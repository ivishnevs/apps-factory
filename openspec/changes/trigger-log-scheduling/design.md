## Context
`Core.Run` ticks every second: expire leases, expire approvals, `ScheduleMessages`, `Schedule`, publish outbox. `Schedule` re-readies retry-eligible FAILED tasks, then for each READY task builds the prompt and calls `Store.Dispatch`, which reserves the task (IN_PROGRESS), inserts run + attempt and the outbox command. The worker GETs the RunSpec, then claims (QUEUED → RUNNING). Automatic holds are encoded in status BLOCKED plus `wait_message_id`, artifact checks and `block_reason`.

## Goals / Non-Goals
Goals: one source of truth for "should this task run now and, if not, why"; no work stranded by a lost command, a restored stream, a reassignment or a closed blocker; a duplicate or stale command never runs.
Non-goals: changing review semantics or retries (later changes), cross-project fairness, mid-run resource slots.

## Decisions

### D1. Trigger log and sequences
```
task_triggers(company_id, seq bigserial, task_id, kind, authority boolean, actor, ref_id, body, created_at)
tasks.wake_seq     = max(seq) of the task's triggers (0 if none)
tasks.handled_seq  = the highest seq consumed by a successful run
```
Every trigger insert and the `wake_seq` update happen in the transaction of their cause. Kinds (text + CHECK; each later change extends the CHECK in its own migration): `assignment` (incl. handoff to the same assignee), `created_ready`, `ready` (owner/authority "run now"), `owner_comment`, `answer`, `dependency_done` (fires once, when the last open dependency becomes DONE), `last_child_closed` (once, when the last open child closes), `child_escalated`, `artifact_approved`, `review_result`. An agent's own comment never triggers its own task. `authority` is true for owner-originated triggers now; `explicit-exits-authority` extends it to managers.

A run records `brief_from = handled_seq` and `brief_to = wake_seq` **at claim**; its brief contains the triggers in `(brief_from, brief_to]` with author and authority flag. A successful run sets `handled_seq = brief_to` (until explicit exits exist, a COMPLETED turn counts as successful; `explicit-exits-authority` replaces this with `ack_seq`).

### D2. Parks
```
task_parks(company_id, id, task_id, kind, ref_id, reason, park_seq, status: active|cleared|overridden, created_at, cleared_at)
```
Kinds: `escalation`, `inspect`, `wait`, `ask`, `block`, `children` (`ask` covers blocking questions and pending action approvals; `block` is a manual block with a reason). `park_seq` = the task's `wake_seq` when the park is created. At most one active park per (task, kind, ref_id). `wait_message_id` and artifact-wait BLOCKED become `ask` parks and the derived `artifacts` reason. Status BLOCKED is no longer stored; the API returns `status: BLOCKED` plus `reason` for compatibility when a non-terminal task has an active park.

### D3. Runnable predicate and reason (pure function)
`internal/tasks/state.go`:
```
runnable(t) = t.status ∈ {READY, IN_PROGRESS, IN_REVIEW, REWORK}
            ∧ t.assignee is a configured agent of the company
            ∧ no live run on t                       (live = QUEUED | CLAIMED | RUNNING | CANCELLING)
            ∧ no active park on t
            ∧ every dependency is DONE
            ∧ every required artifact version is approved
            ∧ now ≥ t.not_before
            ∧ ¬paused(company, project, agent, agent's quota key)
            ∧ t.wake_seq > t.handled_seq
```
**Reason** = the first false conjunct in this order: `terminal` (DONE/CANCELLED), `backlog`, `unassigned`, `live_run`, `parked:<kind>` (first active park in the order escalation, inspect, wait, ask, block, children — what a human must act on comes first), `dependencies`, `artifacts`, `paused:<scope>`, `backoff`, `idle` (wake_seq = handled_seq), `pinned_node_unavailable` (added by `task-worktrees`/`node-agent`), `no_capable_node`, `capacity`; otherwise `runnable`. **Clearer** names who or what clears it (owner, manager X, agent X, task Y, time T). The function takes a snapshot struct (no DB access) so it is unit- and property-testable; storage loads the snapshot in the same transaction that acts on the result. `no_capable_node`/`capacity` are filled from the claim context (`node-agent`); until then capacity = the concurrency caps of D6.

### D4. Dispatcher
Each tick, per company under `pg_advisory_xact_lock(company)`: load snapshots of candidate tasks (`wake_seq > handled_seq`, non-terminal), evaluate, order runnable ones by priority desc, then oldest unhandled trigger, then id; for each, insert run `QUEUED` + attempt + outbox command `agent.run.requested` (payload: run id, company, agent, provider — not the prompt). Status READY/REWORK → IN_PROGRESS on dispatch as today. `active_task_run` keeps one live run per task. The project/workspace writer indexes stay until `task-worktrees`.

### D5. Reconciler
Every `reconcile_interval` (default 30 s):
- a `QUEUED` run whose last command was published more than `redeliver_after` (60 s) ago gets a new outbox command with message ID `<run>/<n>` (a restored or purged stream therefore never strands work; duplicates are harmless because claim is atomic);
- a `QUEUED` run older than `queue_ttl` (15 min) ends with outcome `unclaimed` (no attempt, no backoff); the task is dispatched again on the next tick and its reason shows `no_capable_node` while nothing claims it;
- a runnable task with no live run is simply dispatched by D4 — there is no other path to running work.

### D6. Claim re-checks against the DB
`POST /runs/{id}/claim` runs under the company lock and re-evaluates: run is QUEUED and not revoked; it is the task's live run; `run.agent == task.assignee`; `Evaluate` with the `live_run` conjunct ignored is runnable; OpenSpec reference and approved artifacts (existing checks). It also enforces **concurrency caps**: live runs per agent ≤ `max_concurrent_runs` and per (agent, project) ≤ `max_concurrent_runs_per_project` (both agent YAML; default 1 for agents with role `manager`, so a coordinator never runs in parallel with itself); a cap refusal is `409 {"retry": true}` and the run stays QUEUED. On success it records `brief_from/brief_to`, builds the brief (D8) and returns the full RunSpec with the lease, so the worker no longer GETs the RunSpec separately. On failure the run ends `stale` (no attempt, no backoff) and the response is `409 {"drop": true}`; the worker acks and drops the command. A duplicate command for an already claimed run returns `409 {"drop": true}` as today.

### D7. Owner actions
Assign/reassign, block, unblock, cancel, priority and ready keep expected-version checks. Reassign adds an `assignment` trigger and revokes a QUEUED run of the previous assignee (a RUNNING one is handled by `explicit-exits-authority`). Unblock clears the `block` park; it adds a `ready` trigger only if the owner asks to run now.

### D8. Brief contents and settings
The brief built at claim contains, in order: the previous run's outcome with a diagnostic excerpt (if not `succeeded`); the triggers in `(brief_from, brief_to]` with author, addressee and authority flag; comments since the last run; tasks created by previous runs of this task (`origin_task`, D9) with their status; the branch log (commits of the task branch ahead of the default branch, from the last exit facts once `explicit-exits-authority` and `task-worktrees` exist); and the company and project **rules and facts** (company and project settings: conventions, standing facts, fixed decisions), so agents never act without them. The same settings are available on demand through the MCP tool `factory_settings_get {scope: company|project}` (read-only, run token).

### D9. Agent-created tasks
A task created through MCP records `origin_task` and `origin_run`. Creation is deduplicated: if an open task with the same origin task, the same normalized title (case-folded, whitespace collapsed) and the same assignee role already exists, Core returns that task instead of creating another (`deduplicated: true`). Creation is refused with 409 `cycle` when the given parent and dependencies together would form a cycle (e.g. a dependency that is the parent or reaches it).

### D10. Event feed table
`feed_events(company_id, seq, at, kind, project_id, task_id, run_id, agent, actor, summary, attention, ref)` is introduced here and written in the transaction of the cause (triggers, parks, dispatch, claim refusals, run ends); each row also produces an outbox message on `factory.<company>.feed.<project>.<kind>`. Later changes add their kinds (exits, escalations, node health, costs); `waits-events-cost-cli` adds the attention rules and the Telegram and CLI consumers. The existing project feed endpoint becomes a filtered view of this table.

## Risks / Trade-offs
- Two notions of state (status and parks) → status is the lifecycle, parks are holds; `Evaluate` is the only reader that combines them.
- More rows per task → triggers and parks are small; retention of trigger bodies follows run events (30 days); sequences are kept forever.
- Prompt built at claim rather than dispatch → claim does more work under the lock; bounded by existing context limits (900 KiB).
