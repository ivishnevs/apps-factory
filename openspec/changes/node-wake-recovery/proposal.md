Status: accepted 2026-11-13. Proposed the same day after the owner asked why every project had stopped. Delivery rules
for both node buffers (run events and exits) and late exits of lost runs agreed with the teammate on 2026-11-13; the
owner accepted both decisions below on 2026-11-13.

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

The exit buffer shipped in `eda0bc1` (exits survive a Core outage: factory-mcp leaves `<exit file>.pending`, the node
redelivers it for up to 30 min) has the same weakness as the event spool: `DeliverPendingExit` treats every 4xx as a
final refusal. And a run Core already judged lost loses finished work: its `/agent/exit` is refused with 410
`not_current_run` by the run-credential gateway (`internal/core/mcp.go`), and when Core's `stop: terminal` reaches
the node first, the run's context is cancelled and `awaitExitDelivery` / `redeliverExit` return at once
(`internal/worker/worker.go`), so the exit is never sent, the terminal event releases the run and the task retries
work that was done.

## What Changes

### One delivery rule for both node buffers

- **One refusal classifier**, used by the event spool and by exit delivery (`factorytools/pending.go`, the worker's
  redelivery, the adapter). It classifies by Core's machine-readable reason, not by status code alone:
  - **retry**: network errors, 5xx, 408, 429, and temporary auth refusals: the signature time window, a signature
    refused right after wake;
  - **final**: another node's or an unknown run, invalid data (400, 422), 409 (new input arrived: the exit is stale,
    the input gets a new run), 410 (the run is closed and released), and permanent auth refusals: the lease changed
    (the run token is an HMAC of the lease, so after a new claim it never passes again), the node key revoked, 403
    "agent unavailable" (the agent was deleted).
- **Core names the reason** in every 401/403/410 answer on node and run-credential routes (for example
  `signature_window`, `lease_changed`, `node_key_revoked`, `agent_unavailable`, `not_current_run`), so the node never
  guesses from the text.
- **Bounded retries.** Retries stop at a horizon; past it the node raises a node health event naming the run and what
  is undelivered instead of dropping data silently. A spool is never kept forever.
- **Redelivery is safe.** Exits are keyed by run; run events already skip applied sequences; both stay idempotent.
- **The adapter writes the pending file before it sends an exit** and removes it after Core's answer, so killing the
  process group in the middle of `POST /agent/exit` cannot lose the exit.

### Late exits of lost runs

- **The node delivers a finished exit before it ends a stopped run.** On `stop` the node first stops the runtime (no new
  work), then, if the exit file or the pending file holds an undelivered exit, delivers it through `/agent/exit` with
  its own bounded context (`context.WithoutCancel` plus a timeout) instead of the cancelled run context, and only then
  sends the terminal event. The terminal event is what releases the run, so during the delivery the run is still
  unreleased. No new protocol message: the exit call is the question "accept the finished work?", Core's answer (200 or
  409/410) is the decision.
- **Core accepts a late exit only while the run still holds its task.** The run-credential gateway lets `/agent/exit`
  (only this path) through for a run that is ENDED with outcome `lost` or `timed_out` and `unreleased=1`. `Store.Exit`
  checks in the same transaction that the run is ENDED, lost or timed_out, unreleased, and `task.current_run_id` is
  this run; otherwise it answers 410 and the node records `refused` and drops the exit. The check is transactional so
  it cannot race `releaseLost`. Every other tool and the credential endpoint still refuse a lost run.
- **An accepted late exit closes the run at once**: outcome `succeeded`, `unreleased=0`, no retry. `releaseLost` does
  not schedule a retry for a run with an accepted exit, so releasing on `ended_unacked` (below) and a late exit agree.
- Conversation runs (no task) apply no exits, as today.

### No deadlock and no lost runs after a wake

- **An ended run proves the runtime gone.** When a node lists a run as `ended_unacked`, Core SHALL treat its runtime as
  gone: a lost unreleased run is released (its task retries after the usual backoff unless it has an accepted exit) and
  acked, so the node forgets it, even if its terminal event never arrives. Late terminal evidence still replaces the
  outcome as today.
- **Clock skew after wake does not lose runs** (owner decision 1). A clock-window refusal answers with Core's time
  (`Date` header and the `signature_window` reason). The node keeps an offset to Core's clock, signs with the
  corrected time and retries at once; the offset is bounded (±1 h) and reported in the heartbeat, and a skew above
  120 s raises `node.clock_skew` on the Nodes page once, not per request. Replay protection is unchanged (nonces, the
  120 s window around the corrected time).
- **Liveness counts clock-refused heartbeats for one TTL** (owner decision 2). A heartbeat refused only for the time
  window still proves the node is alive: Core SHALL NOT mark the node offline or its runs lost while such refusals keep
  arriving, for at most one more TTL, so the corrected retry has time to land.

Out of scope: why the Colima VM clock lags after host sleep (a host setup matter; the fixes above make the system
tolerate it), and keeping the laptop awake.

## Owner decisions

Both accepted by the owner on 2026-11-13:

1. Clock correction on the node: yes. The node signs with an offset learned from Core's answer (bounded ±1 h); the
   120 s window and nonce replay protection stay; the owner sees `node.clock_skew` once per episode.
2. Clock-refused heartbeats keep the node alive for one extra TTL: yes.
