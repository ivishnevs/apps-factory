## Context
`Store.Expire` turns RUNNING/CANCELLING runs with `lease_until < now` into UNKNOWN and blocks their task; the `project_writer`, `workspace_writer` and `session_turn` indexes include UNKNOWN, so the project is quarantined until `runs resolve`. Only an explicit FAILED turn may retry, within `max_retries`, and only via a FAILED → READY action. Both Workers (Go Codex, TypeScript Claude) map any error after claim to UNKNOWN and an execution timeout to CANCELLED (Codex `turn/interrupt`, Claude SDK `interrupt()`). Leases: 45 s, heartbeat 10 s, wall clock on both sides.

## Goals / Non-Goals
Goals: no dead state for ordinary work; bounded retries with escalation; uncertain side effects inspected per task; no mass loss after sleep; one place that defines what each outcome means.
Non-goals: exactly-once external effects (still impossible); self-fencing of workers that lost the Core for a long time.

## Decisions

### D1. Run lifecycle and outcome
`runs.status`: `QUEUED → CLAIMED → RUNNING → ENDED` (plus `CANCELLING` while a stop is in flight). `runs.outcome` (text + CHECK) is set exactly once when the run ends. A committed successful completion fixes `succeeded` regardless of what happens to the process afterwards.

| Outcome | Meaning | Attempt | Backoff | Budget |
|---|---|---|---|---|
| succeeded | successful completion committed (after `explicit-exits-authority`: an exit accepted) | no | — | yes |
| no_exit | turn ended without an exit (defined by `explicit-exits-authority`) | yes | — (goes to the manager) | yes |
| failed | runtime/driver error for this task, incl. unexpected process exit and protocol errors | yes | yes | yes |
| timed_out | hard timeout on the sleep-excluding clock | yes | yes | yes |
| idle_timeout | no runtime activity event for `idle_timeout` (default 30 min) | yes | yes | yes |
| max_turns | runtime turn limit | yes | yes | yes |
| setup_failed | task-specific workspace or brief failure (git lock errors are retried, never this) | yes | yes | yes |
| lost | lease or start window expired, worker restarted with a new epoch, terminal evidence never arrived | no | yes | yes, except new-epoch losses |
| revoked | stopped by the owner/authority, cancel, kill switch | no | no | no |
| paused_out | quota limit, auth error, runtime binary missing/incompatible | no | no | no |
| stale / unclaimed | claim re-check failed / never claimed (`trigger-log-scheduling`) | no | no | no |

Worker events map: `turn/completed completed` → succeeded (until exits exist); `failed` → failed; interrupt caused by the hard timeout → timed_out (not CANCELLED); interrupt caused by Core stop → revoked; runtime start/protocol errors → failed; auth/quota/binary → paused_out. The terminal event is `{status: ENDED, outcome}` (runtime-protocol-v2 §8).

Implemented: no terminal evidence (app-server disconnect, shim exit without a result, an interrupt result never received, a node shutdown or lost heartbeat mid-run) → the worker reports outcome `lost`, which Core validates and records as its own `lost` (no attempt, backoff, budget; new-epoch rule unchanged). A timeout with a known cause stays timed_out / idle_timeout without evidence; revoked needs the runtime's evidence and a stop Core asked for, so a node shutdown is never revoked. Migration 0030 stores the phase (ENDED, and CLAIMED instead of deriving it) and maps the old ended statuses to ENDED; it also releases tasks still BLOCKED on a released `run:` from before 0029 as recoverTask would (READY after the backoff), except side-effect tasks and ends that would exhaust the attempts, which stay for the owner.

### D2. Counters and backoff
- `attempts_no_progress` per (task, assignee): incremented by attempt outcomes, untouched by others, reset when the assignee changes, on progress, on authority input and on a `retry` answer. Reaching 3 → escalation `retries_exhausted`.
- `budget_used(task, day)`: budget outcomes per task per company-local day (company setting `time_zone`, default UTC); reaching `daily_budget` (default 30) → escalation `budget_exhausted`. The daily budget is what bounds ping-pong and wait loops; there are no separate counters for them.
- `fail_streak` = consecutive backoff outcomes; `not_before = ended_at + step(fail_streak)` with steps 1, 5, 15 min (then 15). Reset by succeeded, progress, authority input, assignee change or a `retry` answer.
- **Progress** = a successful run that changed the task's recorded result (after `task-worktrees`: an accepted exit whose `exit_sha` differs from the previous one) or new authority input. Authority input also clears `not_before` and `fail_streak`.
- `max_retries` in agent YAML is replaced by these counters (kept as a deprecated alias for the attempts threshold).

### D3. Automatic retry in the same workspace
A task with an attempt or `lost` outcome stays in its status (FAILED is retired; migration maps FAILED → READY with a `ready` trigger). Its triggers are not consumed (`handled_seq` unchanged), so it is runnable again after `not_before`. The next run uses the same workspace and resumes the same runtime session when the driver supports it. The session key is (task, agent): a new assignee starts its own session, `reset-workspace` (`task-worktrees`) starts a new one, and a session is never resumed on another node — a run claimed on a different node starts a fresh session with the brief; the brief starts with "previous run: <outcome>, <diagnostic excerpt>; inspect the workspace before continuing". `project_writer`, `workspace_writer` and `session_turn` no longer include UNKNOWN/lost runs.

### D4. Stop-and-inspect for side-effect tasks only
A task has `side_effects = true` when set by an authority at creation/update or when the assignee's resolved policies contain a rule `type: recovery, mode: inspect` (roles/agents filters as for other rules); the value is snapshotted on the run at claim. For such a task, any outcome other than succeeded, paused_out, stale, unclaimed or a revoke before `RUNNING` creates an `inspect` escalation park on **that task only**; no automatic retry. Answers: `retry` (the effect did not happen or is idempotent), `continue` (the effect happened; the next brief says so), `cancel`. `factory runs resolve` becomes the CLI for these answers. Other tasks, the project and the workspace keep running.

### D5. Escalations
An escalation is a park of kind `escalation` plus an inbox item (existing messaging inbox, type `escalation`) with reason, scope and allowed answers. At most one open escalation per (task, reason). Catalog in this change: `retries_exhausted` (retry, reassign, cancel), `budget_exhausted` (retry — resets today's budget, reassign, cancel), `inspect` (retry, continue, cancel), `stalled` (D8). An answer clears the park and adds an authority `answer` trigger. A `cancel` answer on a task with open children is refused with `needs_cascade` and the escalation stays open; the principal then cancels with cascade. `budget_exhausted` is not opened while the task has authority input above `max(handled_seq, run brief_to)`: that input counts as `retry` (today's budget is reset), so input that arrived just before the limit is never left waiting behind an escalation; input the failing run was already briefed on does not. Target before `hierarchy`: the company's agents with role `manager` (the one with the fewest open escalations, then by name), else the owner; `hierarchy` replaces this with the assignee's manager chain.

### D6. Two-phase claim
`POST /runs/{id}/claim {worker_id, epoch, claim_id}` → `CLAIMED` with lease and `start_by = claim + 60 s`, extended by heartbeats listing the run `claimed`/`starting` up to claim + 15 min (runtime-protocol-v2 §6); the same `(worker_id, epoch, claim_id)` returns the same lease (retry-safe), a lower `claim_id` for that worker/epoch is refused. `POST /runs/{id}/start {lease}` after workspace allocation and runtime launch → `RUNNING` (retry-safe). A CLAIMED run not started in the window ends `lost`. A claim with a newer `epoch` from the same `worker_id` ends the older epoch's unended runs as `lost` (not budget-counted). `worker_id` is a UUID stored in the worker's state directory; `epoch` increments on each worker start.

### D7. Sleep-safe liveness
Core keeps an in-memory **continuity start**: the first tick after Core start or after a gap between ticks longer than 3 tick intervals (the host slept or the process was paused). A lease or start window expires only when `now − max(last_renewal, continuity_start) > ttl`. So after a wake, workers get a full TTL to heartbeat before anything is declared lost. All Core time comes from an injectable `clock.Clock`. On the worker, hard and idle timeouts and the heartbeat schedule use a clock that does not advance during system sleep (Go's monotonic reading where it excludes sleep on the target OS; otherwise `CLOCK_UPTIME_RAW`/`CLOCK_MONOTONIC` via `x/sys/unix`), so a sleeping laptop never wakes up into a timeout. A worker keeps running a run while Core is unreachable and stops only on an explicit stop or a heartbeat answer that the run is terminal.

### D8. Stall detection
The reconciler tick also checks every non-terminal task:
- a `block` or `children` park, or a dependency, on a task that is in BACKLOG or has no assignee → a `stalled` escalation on the waiting task (answers: unblock, cancel) naming the stuck task; it clears itself when the stuck task becomes assigned and not BACKLOG, or closes;
- `idle` alerts: a task with an assignee that has been `idle` (nothing to run, no park) longer than `idle_alert_after` (default 24 h) → one feed event to the assignee's manager;
- no progress: a task with runs but no progress (D2) for `no_progress_alert_after` (default 8 h) → one attention event to the manager.
Each alert is emitted once per episode and re-armed after progress.

## Risks / Trade-offs
- Automatic retry repeats work that partially happened → retries run in the same workspace and session with the previous outcome in the brief; anything with external effects is marked and goes to inspection instead.
- A wrongly unmarked side-effect agent would be retried → the policy rule is attached by role (e.g. release/publisher roles) in the default catalog; protected `system` operations remain approval-gated regardless.
- A paused Core never expiring leases → expiry resumes after one TTL of continuous ticking; tests cover a simulated 2-hour gap.

## Amendments

- 2026-10-20 (D8): stall detection covers a task parked on its children: an open child in BACKLOG or without an assignee opens the same self-clearing `stalled` escalation per (task, child) to the assignee manager; `unblock` releases the parent from its children park, `cancel` cancels it.
- 2026-10-20 (D3): dispatch takes the session of the assignee's last run of the task (the (task, agent) key), not the session of the task's last run; after a review the implementer was handed the reviewer's session, dropped it as another provider's and started fresh (0355740).
