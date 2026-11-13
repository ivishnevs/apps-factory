## 1. The stress test checks the scheduler's rule

- [ ] 1.1 `internal/core/locking_test.go`: replace the hard-coded "one live run per agent" check with a per-agent, per-kind (`spec.conversation`) and per-project count of live runs (QUEUED, CLAIMED, RUNNING, CANCELLING), compared with each agent's caps from the loaded catalog (`storage.AgentRunCaps(...).For(kind)`, 0 = no cap, shared limit counts both kinds)
- [ ] 1.2 On a failure the check logs the offending runs (id, task, agent, kind, status, unreleased) so a real breach is diagnosable from the test output
- [ ] 1.3 Keep the per-task check (no task has two live or unreleased runs) unchanged
- [ ] 1.4 Run `go test ./internal/core -run TestParallelScheduleClaimExpire -count=32` against the test Postgres: no failure

## 2. Deterministic test of the re-check in Dispatch

- [ ] 2.1 Storage test with fixtures (one agent, two projects, caps set in the run's config snapshot): two READY work tasks at `max_concurrent_runs: 1` are both evaluated runnable on snapshots taken before either dispatches; dispatching both queues one run, the other gets `ErrConflict`
- [ ] 2.2 The same for `max_concurrent_runs_per_project`, for conversation runs with `conversation.max_concurrent_runs: 1`, and for `share_work_limit`
- [ ] 2.3 The allowed case: two conversation tasks of one agent in two projects without a conversation cap both queue
- [ ] 2.4 Concurrent variant: two goroutines on separate connections, released together after both hold their snapshots, dispatch the two tasks; exactly one run is queued (repeat in a loop, e.g. 50 iterations)

## 3. State the mechanism

- [ ] 3.1 Comment at the cap check in `Store.Dispatch` (`internal/storage/scheduler.go`) and at `scheduleCompany` (`internal/core/tasks.go`): the snapshot verdict is a pre-filter; the cap is decided by the re-count in `Dispatch`'s company-locked transaction
- [ ] 3.2 Spec delta `postgres-tenancy`: concurrent dispatch decides caps inside the locked dispatch transaction; snapshots may be stale
- [ ] 3.3 If the owner accepts D4-A: one sentence in the agent-config / work-and-conversation-runs spec that unreleased lost runs hold the task and writer slots but not the agent cap

## 4. Follow-up (only if the owner chooses D3-B, separate change)

- [ ] 4.1 `Store.Dispatch` evaluates the full verdict (`taskSnapshotTx` + `tasks.Evaluate`) inside its locked transaction, with the catalog fields Core fills passed in; refuses unless runnable
- [ ] 4.2 `scheduleCompany` reads its pre-filter snapshots with `ViewSnapshot` (no company lock)
- [ ] 4.3 Tests: a stale runnable snapshot does not dispatch past a backoff, an offline pinned node, a held writer slot or a missing secret
