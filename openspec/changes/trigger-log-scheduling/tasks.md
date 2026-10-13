## 1. Model
- [ ] 1.1 Migration: `task_triggers`, `tasks.wake_seq/handled_seq/not_before`, `task_parks`, run `brief_from/brief_to`; backfill READY tasks with one `ready` trigger and BLOCKED tasks with `ask`/`block` parks. Verify: migration test on a fixture with READY, message-blocked, artifact-blocked and manually blocked tasks.
- [ ] 1.2 Write triggers in the transactions of their causes (create with assignee + ready, assign, ready, owner comment, answer, last dependency DONE, last child closed, artifact approved, review result). Verify: unit test per cause checks one trigger and `wake_seq` bump in the same transaction; agent self-comment adds none.
- [ ] 1.3 `internal/tasks/state.go`: `Evaluate(snapshot, now)` with reason order and clearer. Verify: table tests for every reason; property test that exactly one reason is returned and `runnable` iff reason = runnable.

## 2. Scheduling
- [ ] 2.1 Dispatcher in `internal/core/tasks.go` + `storage.Dispatch` using `Evaluate` under the company lock; order by priority, oldest unhandled trigger, id. Verify: scheduler tests for ordering and that idle (`wake_seq = handled_seq`) tasks are not dispatched.
- [ ] 2.2 Reconciler: republish unclaimed QUEUED commands, expire `queue_ttl` runs as `unclaimed`. Verify: JetStream test that purges the command stream; the task still runs after `redeliver_after`.
- [ ] 2.3 Claim re-check, brief at claim, RunSpec returned by claim, `409 {"drop":true}` for stale/duplicate. Verify: tests for reassignment between dispatch and claim, cancelled task, dependency reopened, duplicate command.
- [ ] 2.4 Worker uses the claim response instead of `GET /runs/{id}`; acks on `drop`. Verify: `internal/worker` test with a stale command acks without starting the runtime.
- [ ] 2.5 Concurrency caps per agent and per (agent, project) at claim, default 1 for managers; `409 retry`. Verify: two runnable tasks of one manager run one after the other; the second run stays QUEUED, not dropped.
- [ ] 2.6 Brief contents (previous outcome, triggers with addressee, comments, tasks created by earlier runs, branch log when available, company/project rules and facts) and `factory_settings_get`. Verify: brief golden test; MCP test returns the current settings for the run's company and project only.

## 3. Holds and visibility
- [ ] 3.1 Replace `wait_message_id` and artifact BLOCKED with parks; derive BLOCKED in API responses with `reason`. Verify: messaging and artifact-approval tests pass unchanged at the API level.
- [ ] 3.2 `factory tasks why ID` and `reason`/`clearer` in task GET/list. Verify: CLI test prints the reason for parked, backoff, idle and dependency cases.
- [ ] 3.3 Agent task creation records `origin_task/origin_run`; dedup by origin, normalized title and role; cycle check over parent + dependencies at creation. Verify: duplicate create returns the existing task; parent-plus-dependency cycle → 409 `cycle`.
- [ ] 3.4 `feed_events` table written in cause transactions + outbox subject `factory.<company>.feed.<project>.<kind>`; project feed endpoint as a view. Verify: rollback leaves no feed row; JetStream test delivers each row with a stable `seq`.
- [ ] 3.5 Update `docs/task-lifecycle.md` and `docs/architecture.md`. Verify: docs describe triggers, parks, reasons, reconciler, brief and feed.
