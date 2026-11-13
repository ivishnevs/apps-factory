Status: proposed 2026-11-13 after the owner asked why every project had stopped.

## Why

On 2026-11-13 the owner's laptop, which runs Core (in Colima) and the node `tvorog-mac`, slept on battery from about
07:30 to 15:00, with a maintenance DarkWake every ~15 minutes. Work did not resume when the laptop woke: three
advent-gift tasks (Android parity 2a, 2b, 3a) stayed IN_PROGRESS with no run until both nodes were restarted by hand.
A sleeping laptop is the normal case for nodes on customer machines, so this is a class problem, not one company's.

What happened, from the node log and Core's database:

1. **Signatures refused after wake.** Right after each wake, Core refused the nodes' signed requests with
   `created outside the allowed window: check the node's clock` (37 refusals for `tvorog-mac`, 19 for `mac2`). Retries
   1, 2, 4 and 8 s later failed too, so for at least ~15 s the node's clock and Core's clock were more than 120 s apart.
   The likely source is the Core VM's clock catching up after the host sleeps. Core then saw no heartbeat for a minute,
   marked the nodes offline and their runs `lost`. That breaks run-recovery "Sleep-safe liveness": the node did
   heartbeat within one TTL after wake, but Core refused it.
2. **The node dropped a terminal event on a 401.** The node treats every 4xx on `POST /runs/{run}/events` except 408 and
   429 as final (`internal/worker/events.go`), so a 401 from the clock window deleted the run's spool, terminal event
   included (`run events dropped … refused: Core HTTP 401`). That breaks node-transport "Node event spool" (delete only
   after the terminal event is applied).
3. **Deadlock between node and Core.** Without the terminal event Core never acks the run, so the node lists it as
   `ended_unacked` in every heartbeat. Core releases an unreleased lost run only when the node *stops* listing it
   (`releaseNodeRuns`), and skips `ended_unacked` runs whose end is not applied (`internal/storage/nodes.go`). Each
   side waits for the other; the task stays IN_PROGRESS for good. Only a node restart (a new epoch) releases it.

## What Changes

- **Run events: auth refusals are transient.** The node SHALL keep a run's spool and retry with backoff when Core
  answers 401 or 403 on run events (signature, clock window, revoked lease answers are reported separately); it drops a
  spool only on refusals that can never apply: another node's or an unknown run (404/409 with that reason) and invalid
  events (400/422). A spool kept longer than the retry horizon raises a node health event instead of silently dropping.
- **An ended run proves the runtime gone.** When a node lists a run as `ended_unacked`, Core SHALL treat its runtime as
  gone: a lost unreleased run is released (its task retries after the usual backoff) and acked, so the node forgets it,
  even if its terminal event never arrives. Late terminal evidence still replaces the outcome as today.
- **Clock skew after wake does not lose runs.** A clock-window refusal answers with Core's time (`Date` header and a
  machine-readable reason). The node keeps an offset to Core's clock, signs with the corrected time and retries at once;
  the offset is bounded (±1 h) and reported in the heartbeat, and a skew above 120 s raises `node.clock_skew` on the
  Nodes page once, not per request. Replay protection is unchanged (nonces, the 120 s window around the corrected
  time).
- **Liveness counts refused heartbeats as heartbeats for one TTL.** A heartbeat refused only for the clock window still
  proves the node is alive: Core SHALL NOT mark the node offline or its runs lost while such refusals keep arriving,
  for at most one more TTL, so the corrected retry has time to land.

Out of scope: why the Colima VM clock lags after host sleep (a host setup matter; the fixes above make the system
tolerate it), and keeping the laptop awake.

## Owner decisions

1. Clock correction on the node (signing with an offset learned from Core's answer) — accept, or keep the hard 120 s
   window and only surface `node.clock_skew`?
2. Treating clock-refused heartbeats as liveness for one extra TTL — accept?
