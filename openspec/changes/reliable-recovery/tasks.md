## 1. Outcomes and counters
- [ ] 1.1 Migration: `runs.outcome`, statuses `CLAIMED`/`ENDED`, `start_by`, `worker_id/epoch/claim_id`; task `attempts_no_progress`, `fail_streak`, `side_effects`; `task_budget(task, day, used)`; map UNKNOWN → `lost`, FAILED tasks → READY + trigger; drop UNKNOWN from writer/session indexes. Verify: migration test on a fixture with UNKNOWN and FAILED rows.
- [ ] 1.2 Outcome table as data in `internal/storage` (attempt/backoff/budget flags) applied in one place (`syncTaskRun` replacement). Verify: table-driven test per outcome checks counters, `not_before` and budget.
- [ ] 1.3 Backoff steps, progress and reset rules. Verify: sequence tests (failed, failed, succeeded with progress, failed) produce 1 min, 5 min, reset, 1 min.
- [ ] 1.4 Escalations `retries_exhausted`, `budget_exhausted` as parks + inbox items; answers retry/reassign/cancel; `cancel` with open children → `needs_cascade`; routing to role `manager`, else owner. Verify: three failed attempts park the task and a `retry` makes it runnable with counters reset; a company without a manager routes to the owner.
- [ ] 1.5 `budget_exhausted` not opened with authority input above `handled_seq` (treated as retry); company `time_zone` for the budget day. Verify: test with a directive arriving during the budget-exhausting run; day boundary test in a non-UTC zone.
- [ ] 1.6 Stall detection: `stalled` escalation for waits on BACKLOG/unassigned tasks (self-clearing), idle and no-progress alerts once per episode. Verify: fake-clock tests for each case, including automatic clearing.

## 2. Side-effect inspection
- [ ] 2.1 Policy rule `type: recovery, mode: inspect` in `internal/policies` + task `side_effects` field; snapshot on the run. Verify: config validation tests; `factory agents authorize` shows the effective mode.
- [ ] 2.2 `inspect` escalation per task with answers retry/continue/cancel; `factory runs resolve` maps to them. Verify: a lost run of a side-effect task parks only that task while another task of the same project is dispatched.

## 3. Claim and liveness
- [ ] 3.1 Two-phase claim with idempotent `(worker_id, epoch, claim_id)` and `POST /runs/{id}/start`. Verify: retried claim returns the same lease; unstarted claim ends `lost` after 30 s; new epoch ends old runs `lost` without budget.
- [x] 3.2 Core continuity guard and injectable clock in the tick loop; expiry uses `max(last_renewal, continuity_start)`. Verify: fake-clock test with a 2-hour tick gap expires nothing until one TTL of continuous ticks without heartbeats. Done: 8928ee0.
- [ ] 3.3 Orphan safety before retry: each run records its runtime process group in a pidfile; a worker starting with a new epoch kills and reaps the process groups of its previous epoch's runs before claiming, and a `lost` run is redispatched only after its worker has reported that group gone or a new epoch of that worker has registered. Verify: test kills the worker mid-run, restarts it and asserts the old runtime process is gone before the retry claims; a lost run of an unreachable worker stays unclaimed until then. Partial: Worker-local pidfile and orphan kill at worker start done (2499b4d); epoch in Core and redispatch gating pending.
- [ ] 3.4 Worker: sleep-excluding clock for hard/idle timeouts and heartbeats; timeout → `timed_out`, Core stop → `revoked`, runtime errors → `failed`; never emit UNKNOWN. Verify: worker tests with the fake RPC server for each mapping; a clock test documents the chosen source per OS. Partial: Sleep-excluding clock pinned for Go and TS workers done (2499b4d); outcome mapping pending.

## 4. Retry flow and docs
- [ ] 4.1 Retry resumes the same workspace/session keyed by (task, agent); another node or a workspace reset starts a fresh session; brief starts with the previous outcome. Verify: fake-RPC test sees `thread/resume` with the previous thread and the outcome line; a claim from another node sees `thread/start`.
- [ ] 4.2 Update README "UNKNOWN" section, `docs/architecture.md`, `docs/task-lifecycle.md`, `docs/runtime-protocol.md`. Verify: docs no longer describe project quarantine for ordinary tasks.
