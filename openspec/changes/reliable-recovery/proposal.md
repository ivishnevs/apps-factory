## Why
Today a lost worker, an expired lease or a crash after claim makes the run UNKNOWN, blocks the task and quarantines the whole project, workspace and session until the owner reconciles it by hand; a timeout interrupts the turn and the task is effectively cancelled. That is the right default for actions with external side effects, but for ordinary work it turns every laptop sleep, network blip or runtime crash into owner work and stops unrelated tasks of the same project. Risks this change removes: a timed-out run that the system refuses to run again strands uncommitted work; a recovery loop kills live runs or leaves runs "running" forever; and a sleeping laptop expires every lease at once on wake.

## What Changes
- A **run outcome table** replaces COMPLETED/FAILED/CANCELLED/UNKNOWN as the input to recovery; each outcome fixes whether it is an attempt, whether it backs off and whether it counts against the daily budget.
- Two counters per task: **attempts without progress** (3 in a row → escalation `retries_exhausted`) and a **daily budget** of budget outcomes (default 30 → escalation `budget_exhausted`). **Backoff** 1, 5, 15 min via `not_before`.
- Ordinary tasks are **retried automatically in the same workspace, resuming the session**; the brief states the previous outcome. A **timeout is a failed attempt**, not a cancellation.
- **Stop-and-inspect only per task, only for tasks with external side effects** (marked by policy or on the task): an uncertain outcome parks that task with an `inspect` escalation; the project, workspace and other tasks keep running.
- Escalations go to an agent with role `manager`, else the owner, until `hierarchy` routes them up the chain; a `stalled` escalation and idle/no-progress alerts catch work waiting on unassigned or BACKLOG tasks.
- **Two-phase claim** (claim, then start within 30 s) with an idempotent claim key.
- **Sleep-safe liveness**: Core never expires a lease or start window until it has itself been running continuously long enough after a pause; worker timeouts use a clock that does not advance during system sleep.

## Out of scope
Explicit exits and `no_exit` handling (`explicit-exits-authority`); routing escalations up reporting chains (`hierarchy` — until then they go to a `manager`-role agent, else the owner inbox); node heartbeat with run inventory (`node-agent`); quota-key pauses (`runtime-drivers`).

## Dependencies
`postgres-tenancy`, `trigger-log-scheduling` (parks, `not_before`, `Evaluate`, claim re-check).

## Capabilities
### New Capabilities
- `run-recovery`: Outcome table, attempt counters, backoff, automatic retry, per-task inspection for side-effect work, two-phase claim and sleep-safe liveness.

### Modified Capabilities
None (replaces the UNKNOWN quarantine and timeout-as-cancel rules described in the foundation and task-engine changes).

## Impact
`internal/storage` (`scheduler.go` `Expire`/`syncTaskRun`/`Resolve`, `storage.go` `Claim`/`Heartbeat`, schema indexes), `internal/core` (`core.go` tick loop, continuity guard, new `/runs/{id}/start`), `internal/worker` and `workers/claude` (outcome mapping, monotonic timers, two-phase claim; the Claude service until `runtime-drivers` moves it under the Go driver), `internal/policies` (recovery rule), `internal/tasks` (FAILED retired), `cmd/factory` (`runs resolve` → inspect answers), docs.
