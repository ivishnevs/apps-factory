# Node troubleshooting: sleeping hosts, clock skew, delivery (node-wake-recovery)

Nodes run on people's own machines, and laptops sleep. This page explains what a node and Core do when a host sleeps
and wakes, when their clocks are apart, and when Core refuses something the node must deliver, and what the owner
sees in each case. The contract of the exits themselves is in [explicit-exits-contract.md](explicit-exits-contract.md).

## Sleeping hosts

- **Timers do not count sleep.** Run timeouts, idle timeouts and the heartbeat schedule run on a clock that stops
  during system sleep, and a run's `elapsed_ms` in the heartbeat excludes sleep, so a laptop never wakes into a
  timeout.
- **Core judges nothing right after its own wake.** A node is offline after `NodeOffline` (60 s) without a heartbeat,
  and a run's lease is the same 60 s; Core judges both only after it has been ticking continuously for one window,
  so a Core host that slept gives its nodes a full window to check in.
- **A node that heartbeats within one window after the wake loses nothing.** If its heartbeats are refused only
  for the signature time window (next section), Core still counts them as contact for at most one more window (below).
- **A node silent for longer** goes offline (`node.offline`, with attention after 10 min) and its unended runs end
  `lost`. When it comes back, its heartbeat stops what Core ended. A lost run whose runtime is still running is
  *unreleased*: its task waits until the node proves the runtime gone, by no longer listing the run or by listing it
  `ended_unacked`; then the run is released (and acked) and the task retries after the usual backoff, unless the
  run's late exit was accepted (see [Late exits of lost runs](#late-exits-of-lost-runs)).

## Clock skew

Every node request is signed (node-identity D4) with its creation time; Core accepts a signature created within
`nodesig.Window` (120 s) of its own clock, once per nonce. After a host wakes, the clocks of Core (often a VM, such as
Colima, whose clock catches up late) and of the node can be minutes apart.

- **Core answers its time.** A window refusal is `401` with `{"error": "...", "reason": "signature_window",
  "core_time_ms": <Core's time in Unix ms>}` and a `Date` header from Core's clock.
- **The node corrects and retries at once.** It keeps an offset to Core's clock (`worker.Clock`, one per node
  process, shared by heartbeats, polls, run events and exits), signs later requests with its time plus the offset,
  and retries the refused request once, immediately. The offset is bounded to ±1 h (`worker.MaxClockOffset`): beyond
  that the node does not correct, it logs an error and keeps reporting the offset; fix the clocks.
- **Replay protection is unchanged**: nonces and the 120 s window around the corrected time.
- **The node reports the offset** in its heartbeat, `health.clock_offset_ms` (Core's clock minus the node's; absent
  when the clocks agree). When Core later accepts a request signed with the current offset, an observation beyond
  the bound is over. When the clocks converge again, the next request is refused once, the node learns the new
  offset (near 0) and the report follows.
- **The owner sees** `node.clock_skew` (attention) once per episode when the reported offset is above the window,
  and `node.clock_ok` when it is back within it. The Nodes page shows the offset on the node's card ("Clock: Core 5m
  0s behind"). Refused signatures still raise `node.signature_refused` (at most one a minute per node); a node whose
  clock is more than an hour off keeps raising it, because its heartbeats never pass.
- **Liveness during the refusals.** A heartbeat Core refuses only for the window still proves the node alive. Core
  credits it as contact, never later than one `NodeOffline` past the last accepted heartbeat, and keeps the node's
  live runs' leases for as long: the node and its runs stay for at most one more TTL, so the corrected heartbeat has
  time to land. Refusals alone never keep a node online past that, and they never bring an offline node back.

What to fix on the host: keep the clocks synchronised (NTP on the node; for Core in a VM, the VM's time sync after the
host wakes). The system tolerates minutes of skew; the event tells the owner it happened.

## The delivery rule

A node delivers two kinds of data Core must not lose: a run's events (the node's event spool, one `events.jsonl` per run in
its state directory) and an agent's exit (factory-mcp's exit with its `<exit file>.pending` copy, and the node's redelivery).
Both follow one rule (`internal/delivery`), by Core's machine-readable reason and the HTTP status:

| Answer | Verdict | What the node does |
|---|---|---|
| network error, 5xx, 408, 429 | retry | keeps the data and retries with backoff |
| 401/403 `signature_window`, `signature_before_start`, `signature_replay`, `signature_invalid` | retry | signs anew (with the corrected clock) and retries |
| 401/403 without a known reason | retry | retried up to the horizon: dropping data on a guess is worse |
| 400, 404, 409, 410, 422, other 4xx | final | drops the data and records the refusal |
| 401/403 `lease_changed`, `node_key_revoked`, `node_key_unknown`, `node_key_invalid`, `node_forbidden`, `agent_unavailable`, `unknown_run`, `run_not_live`, `credential_missing` | final | drops the data and records the refusal |

- Core names the reason in every 401, 403 and 410 of node and run-credential routes (`{"error", "reason"}`).
- **Retries are bounded** by `delivery.Horizon` (30 min). Past it nothing is dropped silently: the node's self-check
  `delivery.events` fails, naming the runs whose events are undelivered (`node.check_failed` on Core, attention), and
  an exit left pending past `ExitRedeliverWait` (30 min) ends its run with `exit_state: pending`, which Core records
  as `lost` (the task retries; the work is on the run's branch).
- **Redelivery is idempotent**: run events skip applied sequences, exits are keyed by run.
- **The adapter writes the pending file before it sends an exit** and removes it after Core's answer, so killing the
  process group mid-request cannot lose the exit.

## Late exits of lost runs

Core may judge a run lost while it still runs (its node was silent). When the run then finishes, its exit is not
thrown away:

- On `stop`, the node first stops the runtime, then delivers an undelivered exit (the exit file says pending, or the
  pending file is there) with its own bounded context (`StopExitDeliveryWait`, 2 min), and only then sends the
  terminal event. The terminal event is what releases the run, so the run still holds its task during the delivery.
- Core lets `/agent/exit` (and only it) through for a run that is ENDED `lost` or `timed_out` and unreleased, and
  accepts the exit only while the run is still the task's current run, in one transaction with the release. An
  accepted late exit closes the run as `succeeded`, released, with no retry; otherwise Core answers 410 and the node
  records the exit as refused. Conversation runs apply no exits.

## What the owner sees

| Situation | Feed / Nodes page |
|---|---|
| Clocks apart after a wake | `node.signature_refused` for the first refusal, then one `node.clock_skew` (attention); the node card shows the offset; `node.clock_ok` when the clocks agree again |
| Clock more than 1 h off | `node.signature_refused` once a minute and the node going offline; fix the clock |
| Node silent past one window (or two with clock refusals) | `node.offline`, its runs `lost`; `node.online` when it is back |
| Events undelivered past 30 min | `node.check_failed` `delivery.events` naming the runs |
| Lost run finished meanwhile | the task moves on with the run's exit (run `succeeded`), or retries if Core refused it |
