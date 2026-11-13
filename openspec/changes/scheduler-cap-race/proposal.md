Status: proposed 2026-11-13, for the owner's decision.

## Why

`TestParallelScheduleClaimExpire` (`internal/core/locking_test.go`) fails about 1 run in 16 on main with
`check: an agent has more live runs than its cap` (`locking_test.go:137`, reported at `:145`). It fails the same
before and after 8f12782 (views read without the company lock). With the scheduler's own `TaskSnapshot` also read
without the lock, it fails in most runs. That looked like dispatch relying on snapshot state read before
`Store.Dispatch`'s transaction, so that two dispatches could each see an agent below its cap.

**The investigation found no cap race in the scheduler. The test's check is wrong.** Each failure is two
**conversation** runs of the same agent, in two projects, both live at once. The scheduler allows this by design:

- Every failing run dumped the same pair: `conv-calorie-manager-p` and `conv-calorie-manager-q`, both
  `spec.conversation=true`, QUEUED+RUNNING or QUEUED+QUEUED (2 of 2 failures in 16 runs on main, 5 of 5 with lock-free
  snapshots). No failure involved a work run.
- `calorie-manager` is the boss (`reports_to`) of `calorie-ios`, `calorie-qa` and `calorie-designer`
  (`configs/agents/*.yaml`). Their tasks in the test are spread over projects `p` and `q`. The test's run ends fail
  those tasks, and the notices wake the manager's conversation task in each project
  (`conv-<agent>-<project>`, `internal/storage/conversations.go:22`).
- work-and-conversation-runs, Decision 3 (owner 2026-11-08): conversation runs do not count against
  `max_concurrent_runs`, and by default there is no cross-project conversation cap
  (`spec.execution.conversation.max_concurrent_runs`, 0 = none). `RunCaps.For(conversation=true)` returns that cap
  (0) and `kindConversation` (`internal/storage/evaluate.go:394-403`). `overCaps` treats 0 as "no cap"
  (`evaluate.go:406`). `Evaluate` makes a conversation wait only on its own cap, when one is set
  (`internal/tasks/state.go:449-456`).
- The test's check (`locking_test.go:130-138`) predates that change. It counts every live run with a `task_id` per
  agent, of both kinds, against a hard-coded cap of 1 ("Every agent's cap is one live run"). So it reports a
  violation when the manager answers in `p` and `q` at once.
- Control: in a scratch copy, the check was made to count by run kind (work runs only against
  `max_concurrent_runs`). With the scheduler's `TaskSnapshot` also switched to the lock-free read, 0 of 24 runs
  failed, where 5 of 8 had failed with the old check. With the locked snapshot (main), the kind-aware check also
  passed 32 of 32. The lock-free read made the false failure more frequent, not more real: ticks no longer wait for
  the lock, so both conversation tasks are more often dispatched in the same window.

### How the cap is enforced today (end to end)

1. **Pre-filter on a snapshot.** `Core.scheduleCompany` (`internal/core/tasks.go:666`) lists READY tasks. For each
   one it takes `Store.TaskSnapshot` (`tasks.go:678`; its own locked transaction, `evaluate.go:16-23`), fills the
   caps from the catalog in `evaluateIn` (`tasks.go:591-598`: `AgentCap`, `AgentProjectCap`, `AgentConversationCap`,
   `AgentSharedLimit`) and keeps the tasks with a `runnable` verdict (`tasks.go:682`). The snapshot counts the
   assignee's live runs on *other* tasks per kind (`evaluate.go:283-299`, `agentLive` at `:328-332`: `status!='ENDED'`,
   i.e. QUEUED, CLAIMED, RUNNING, CANCELLING). Review runs of IN_REVIEW tasks take the same path (`tasks.go:709`,
   `:718`). The snapshot's transaction commits before any dispatch starts, so the verdict can be stale.
2. **Decision under the company lock.** `dispatchTask` (`tasks.go:738`) calls `Store.Dispatch` (`tasks.go:765`). The
   transaction takes the per-company advisory lock (`Store.begin`, `internal/storage/storage.go:193-206`;
   `lockCompanyTx`, `internal/storage/db.go:132`, D4) and checks the task version and status (`scheduler.go:34`). It
   then **re-counts the agent's live runs of the run's kind in SQL and refuses at the cap**
   (`internal/storage/scheduler.go:73-82`, `ErrConflict`, and the scheduler moves on). It also refuses while the
   task's current run is live or unreleased (`scheduler.go:108-116`). The run is inserted in the same transaction
   (`scheduler.go:138`). Postgres runs these transactions at READ COMMITTED, so every statement after the lock sees
   all commits made before the lock was granted. Two replicas therefore cannot both pass the check for the same slot:
   the second waits for the lock and then counts the first one's QUEUED run.
3. **Claim.** `ClaimV2` re-checks the caps over *started* runs (CLAIMED, RUNNING, CANCELLING) of the kind
   (`internal/storage/nodes.go:548-556`, `agentStarted` at `evaluate.go:336-339`), so a run is never started over the
   cap even if it was queued.
4. **What lowers the count.** Run end (`Apply` → ENDED), `Expire` (lease expired: `endLost` / `endLostRun`,
   `nodes.go:368-385`, the run turns ENDED, possibly `unreleased=1`), `ExpireQueued` (`claim.go:63`, QUEUED → ENDED),
   `StopIdle`, and the heartbeat's release of lost runs (`unreleased=0`). None of them moves a run back to a live
   status. The only code that inserts a live run is `insertRun`, called from `Dispatch` and from `Store.Create`
   (direct run creation, not the scheduler). Each of these steps takes the company lock, so it is ordered against
   `Dispatch`.

So a stale snapshot can only make a task *try* to dispatch. The cap itself is decided inside `Dispatch`'s locked
transaction.

### What remains true and is worth fixing

- The test does not test the rule the scheduler implements (run kinds, per-project caps, the shared limit), and it
  hard-codes one cap for every agent. It is flaky on main and fails on any change that dispatches faster.
- Nothing deterministic pins "Dispatch re-checks the cap whatever the snapshot said". `TestConcurrencyCaps`
  (`internal/storage/task_engine_test.go:308`) and `TestConversationLimits`
  (`internal/storage/conversation_runs_test.go:133`) dispatch one task after another, so neither has a stale
  verdict. Work to move dispatch reads off the lock (8f12782 left `TaskSnapshot` locked for dispatch) has no test
  that would catch losing the re-check.
- `Dispatch` re-checks only part of the verdict: version and status, parks (`askPark`), pauses including the quota
  key, the task pause, approved artifact inputs, dependencies, caps, and the current run. It does **not** re-check
  the node conditions (`nodeReason`: pinned node offline, paused or full; no capable node), the backoff
  (`not_before`), new input (`WakeSeq > HandledSeq`), the project writer slot, project setup, or a missing secret.
  Most of these are caught later: a version bump makes the dispatch conflict, and the claim re-checks node fit and
  the writer slot. But a dispatch from a stale snapshot can still queue a run the fresh verdict would hold. Today the
  locked snapshot keeps this window short, and the window is the reason not to move the snapshot off the lock
  without the change in Decision 3.

## What changes

1. **The stress test checks the scheduler's rule, not a hard-coded cap.** The check counts live runs per agent and
   per kind, and per project where a per-project cap applies, against each agent's caps from the catalog
   (`storage.AgentRunCaps` of the agent config, `.For(kind)`), including the shared limit. It no longer assumes that
   every agent's cap is 1. The per-task check ("no task has two live runs", unreleased runs included) stays.
2. **A deterministic test pins the re-check in `Dispatch`.** Two READY tasks of one agent at cap 1 are both evaluated
   runnable on snapshots taken before either dispatches (the stale verdicts the race needs). Then both are
   dispatched in turn: the first queues, the second gets `ErrConflict`. The test repeats the same for the per-project
   cap, for conversation runs with `conversation.max_concurrent_runs: 1`, and for the shared limit. It also shows the
   allowed case: two conversation runs of one agent in two projects, without a conversation cap, both queue. A second
   variant makes the race real: two goroutines dispatch the two tasks at once on separate connections, gated so that
   both have their snapshot before either starts, and exactly one run may be queued.
3. **The reliance is stated where it lives.** A comment at `Dispatch`'s cap check (`scheduler.go:73`) and at
   `scheduleCompany` (`tasks.go:674`) says that the snapshot verdict is a pre-filter, and that `Dispatch`'s locked
   re-count is the cap decision.
4. **No change to scheduling behaviour** (Decision 2), and the dispatch snapshot stays locked for now (Decision 3).

## Decisions

### D1. How the stress test's check counts

- **A. Per agent and kind against the catalog caps (recommended).** Query live runs grouped by agent, kind
  (`spec.conversation`) and project. Compare them with `AgentRunCaps(agent config).For(kind)`, the shared limit
  included, and treat 0 as no cap. It mirrors the rule of work-and-conversation-runs, holds for any agent in
  `configs/agents`, and still fails if a real cap breach appears.
  Risk: the check duplicates the counting rule. Keep it small and use `RunCaps.For` and `overCaps` semantics, so
  that a later change of the rule shows up in both places.
- **B. Exclude conversation runs from the check.** One line. It misses breaches of a configured conversation cap and
  of the shared limit, and it still hard-codes cap 1.
- **C. Give the test's agents a conversation cap of 1.** This changes the fixture instead of the check, and it no
  longer exercises the default configuration (no conversation cap) that most companies run.

### D2. Harden `Dispatch` against a cap race anyway?

- **A. No change. The company lock plus the in-transaction re-count is the mechanism (recommended).** Pin it with the
  deterministic test from What changes 2 and the comment from 3. Every deciding transaction already serializes per
  company (D4, postgres-tenancy). There is no window in which two dispatches both pass the count.
  Risk: the guarantee depends on `Dispatch` and `ClaimV2` keeping the company lock. If that lock is ever split for
  throughput, the test from What changes 2 fails and points here.
- **B. A per-agent advisory lock** (`pg_advisory_xact_lock(lockAgent, hashtext(company||'/'||agent))`) in `Dispatch`
  and the claim. Redundant while the company lock exists. It becomes the right tool if the company lock is ever
  narrowed. Risk: lock ordering with the company lock, and one more lock per dispatch.
- **C. A database constraint.** A per-agent count cap cannot be a plain unique index (caps above 1, per kind, per
  project, and taken from the run's config snapshot). It would need a trigger that counts under a row lock, which
  duplicates the Go rule in PL/pgSQL. Not recommended.
- **D. Idempotent dispatch keys** (one queued run per task and version). This prevents two runs of one task, which
  the version check and the `CurrentRunID` check already prevent. It does nothing for two tasks of one agent. Not
  relevant to the cap.

### D3. May the scheduler's snapshots be read without the company lock?

Background: 8f12782 kept `TaskSnapshot` locked for dispatch. Lock-hold logging (`slow_company_lock`) may later show
that `scheduleCompany` (one locked transaction per READY task, 10-20 queries each) dominates lock time.

- **A. Keep them locked in this change (recommended now).** No behaviour change. Measure with `slow_company_lock`
  first.
- **B. Read the snapshots lock-free, and let `Dispatch` evaluate the full verdict inside its locked transaction.**
  `Dispatch` runs `taskSnapshotTx` and `tasks.Evaluate` on the same transaction and refuses unless the verdict is
  `runnable` (or, for a review run, not a stale verdict). Core passes the catalog fields that `evaluateIn` fills (caps,
  missing secret, quota key) as an argument. This makes the decision one transaction, closes the
  not-re-checked conditions listed in Why, and lets the pre-filter scan stay off the lock.
  Risk: `Dispatch` holds the lock longer (one snapshot per dispatched task, not one per READY task). This is still a
  net win, because most READY tasks are not runnable. The snapshot code becomes part of the locked path.
- **C. Read lock-free and rely on today's re-checks.** The caps are safe (shown by the control run), but a stale
  verdict can queue a run despite a backoff, a pinned node going offline, or a missing secret. Not recommended.

Recommendation: A now. B as its own change when lock time shows the scheduler's snapshots as a top holder.

### D4. Unreleased lost runs and the agent cap

A lost run whose runtime may still run (`status='ENDED' AND unreleased=1`) keeps its task's slot (`scheduler.go:114`)
and the project writer slot (`evaluate.go:319`). It does **not** count against the agent's `max_concurrent_runs`
(`agentLive`, `agentStarted`: `status!='ENDED'` only). So after a lease expiry the agent can get a new run while the
old runtime may still be alive.

- **A. Keep it and document it (recommended).** The writer slot already prevents two writers in one workspace. A
  node that holds a lost runtime confirms it gone on its next heartbeat. Counting it would block the agent in every
  project until a node or the owner releases it.
- **B. Count unreleased runs against the agent cap.** Strict, but one silent node blocks the agent everywhere.

## Impact

- Specs: `postgres-tenancy` (the concurrent-dispatch requirement states that caps are decided in `Dispatch`'s locked
  transaction and that snapshots are a pre-filter). `work-and-conversation-runs` / `agent-config` is unchanged; D4,
  if accepted, adds one sentence about unreleased runs.
- Code: `internal/core/locking_test.go` (kind-aware check). New deterministic tests in
  `internal/storage/task_engine_test.go` or `internal/storage/conversation_runs_test.go`. Comments in
  `internal/storage/scheduler.go` and `internal/core/tasks.go`. No production logic changes under the recommended
  options (D3-B would change `Store.Dispatch` and `Core.dispatchTask` in a later change).
- Data: no migration.
- Every company: the rule checked is the catalog's per-agent caps by kind. No company, project or agent is named in
  the tests' rule; fixtures set the caps. The stress test keeps using `configs/agents` as one real configuration.
