## Why
Today a task runs because its stored status is READY and the scheduler picks it; why it does *not* run is spread across status, `wait_message_id`, artifact blocks, writer indexes and retry flags. The expensive failures of our scheduler sit exactly here: a handoff that changes the assignee but loses the "wake", queued runs dropped on reassignment, wakes silently skipped, blocked work never re-checked after its blocker closed, and nobody able to say why an item is idle. A durable command on the bus is necessary but not sufficient: a lost or restored stream must never strand work.

## What Changes
- Add a **trigger log**: every reason to run a task is a row in `task_triggers`, written in the same transaction as its cause, and bumps `tasks.wake_seq`. `tasks.handled_seq` records what the last successful run consumed. The bus carries commands; the database is the truth.
- Add **parks** (`task_parks`): every automatic reason to hold a task (question, approval, manual block, children, and later wait/escalation/inspect) is an explicit row; BLOCKED becomes a derived view, not a stored status.
- Add one pure function `tasks.Evaluate(snapshot, now) → (runnable, reason, clearer)` with a fixed reason order; the scheduler, claim and owner CLI all use it.
- Rework the scheduler into a **dispatcher** (creates a QUEUED run + outbox command for runnable tasks) and a **reconciler** (re-publishes commands for QUEUED runs nobody claimed, expires stale QUEUED runs, so every runnable task without a live run gets a command again).
- **Claim re-checks** the task against the database, including per-agent and per-(agent, project) concurrency caps; a stale command is dropped without an attempt. The run brief is built at claim: previous outcome, triggers since `handled_seq` with addressee, tasks created by earlier runs, branch log, and the company and project rules and facts (also readable via `factory_settings_get`).
- Agent-created tasks are **deduplicated** by origin, normalized title and role, and refused when parent and dependencies form a cycle.
- The **event feed table** (`feed_events` + outbox to NATS) starts here, so every later change writes its events into it.

## Out of scope
Run outcomes, backoff and retry budgets (`reliable-recovery`); explicit exits and the 409 rule (`explicit-exits-authority`); node capacity at claim (`node-agent`); timer waits (`waits-events-cost-cli`). Message delivery runs keep their current scheduling until `hierarchy` replaces them with conversation tasks.

## Dependencies
`postgres-tenancy` (company scope, advisory lock per company).

## Capabilities
### New Capabilities
- `trigger-scheduling`: Trigger log, parks, runnable predicate with reasons, dispatcher, reconciler and claim re-check.

### Modified Capabilities
None (replaces the READY-only selection described in the task-engine change).

## Impact
`internal/tasks` (new `state.go`, statuses), `internal/storage` (`scheduler.go`, `tasks.go`, `messages.go`, `artifacts.go`, new migration), `internal/core/tasks.go` (`Schedule` → dispatcher/reconciler, brief at claim), `internal/core/core.go` (claim response), `internal/factorytools` (`factory_settings_get`), `internal/worker` and `workers/claude` (use the RunSpec from the claim response; commands carry only a run pointer, which also removes the 900 KiB broker budget on run context), `cmd/factory` (`tasks why`), docs `task-lifecycle.md`.
