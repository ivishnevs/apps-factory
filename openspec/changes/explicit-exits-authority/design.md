## Context
`factory_task_submit` saves a draft; known runtime completion moves the task to IN_REVIEW; reviewers call `factory_task_accept/reject`. `cmd/factory-mcp` is a per-run stdio adapter that forwards tool calls to `/agent/call` with the run token. Owner messages are delivered by separate message runs. After `trigger-log-scheduling` every run has a brief range `(brief_from, brief_to]`.

## Goals / Non-Goals
Goals: every run ends in a known way; nobody finishes against input they have not seen; managers and the owner can redirect running work; noise from non-authority comments never costs a run.
Non-goals: forcing agents to ack every comment; preventing a trusted agent from bypassing MCP in the shell.

## Decisions

### D1. Exit tools
| Tool | Effect when accepted |
|---|---|
| `factory_exit_handoff {to, note, verdict?, feedback?}` | assignee := `to` (may be the same agent), `assignment` trigger. From IN_REVIEW with `verdict`: `approved` → back to the implementer, IN_PROGRESS, approval recorded for the task's pull request `{pr_number, approved_head}` (from the exit facts); `changes_requested` (feedback required) → REWORK to the implementer. |
| `factory_exit_submit {reviewer?, summary, checks, artifacts}` | IN_REVIEW, assignee := reviewer (default: per policy, else the assignee's manager), implementer recorded; structured submission stored (replaces the draft). |
| `factory_exit_done {summary}` | DONE (see D4). On a conversation task (`hierarchy`) it means "handled up to `ack_seq`" and the task stays open. |
| `factory_exit_ask {question, options?, category, evidence?}` | `ask` park + decision routed to the deciding principal (owner now, manager chain after `hierarchy`). |
| `factory_exit_block {on: [task ids] \| on_children: true, reason}` | `block` park on open tasks without a cycle, or `children` park with ≥ 1 open child; otherwise converted to `ask`. |
| `factory_exit_wait {reason, wake_at, deadline}` | `wait` park; rules in `waits-events-cost-cli`. |

The old `factory_task_submit`, `factory_task_accept`, `factory_task_reject` are removed from the agent tool list; the owner review endpoint maps to a verdict handoff. A run has at most one accepted exit (`run_exits` keyed by run id); a retry returns the stored result. After an accepted exit the worker lets the turn finish and stops the runtime if it is still running 30 s later; anything written after the exit is not part of the exit (see `task-worktrees`).

### D2. Sequences and the 409/410 rules
- `max_authority_seq(t)` = the highest seq of an authority trigger of `t`.
- `ack_seq` is maintained **by the MCP adapter** as the highest trigger seq it has returned to the runtime: the brief range end at start, any `authority_input` or `triggers` field in a tool response, and 409 bodies. A 409 that never reached the runtime therefore never counts as seen. Every exit carries it.
- **410 Gone** if the calling run is not the task's current live run (revoked, ended, superseded): no state change; the adapter tells the agent to stop; the next brief mentions it.
- **409 Conflict** with no state change if `max_authority_seq > ack_seq`; for `done` also if `wake_seq > ack_seq`. The body lists the unseen triggers; the adapter returns them to the runtime and raises `ack_seq`; retrying the exit is then allowed. Retries are safe and uncapped.
- An accepted exit sets `handled_seq = ack_seq` and the run outcome `succeeded` in the same transaction; parks created by the exit get `park_seq = wake_seq`.

### D3. Exit facts
`cmd/factory-mcp` runs in the run's working directory on the node and, for every exit, collects `{head, dirty, branch, ahead_of_default, pushed, pr: {number, state, head_sha}}` with `git` and the project's forge CLI (`gh`). Dirty = tracked changes or untracked non-ignored files. Facts are stored on the exit; Core never reads the disk. `exit_sha = head`. Progress (`reliable-recovery`) uses `exit_sha` changes.

### D4. Done and merged pull requests
`done` is refused (409, hint "merge your PR first: rebase on the default branch, resolve conflicts, build, merge") when the task has a branch with commits ahead of the default branch and its pull request does not have `pr.state == merged`, unless an authority answered `abandon_branch`. If the project's policy requires review, `done` also requires an approval bound to that pull request (`pr_number`). Commit ancestry is not used, so rebase and squash merges pass. If the PR head changed after approval, project policy `review_after_conflicts` (`task-worktrees` D7) decides: `required` — `done` is refused unless an approval exists for the PR's last head before merge (a new `submit` before merging); `agent_decides` — the implementer re-submits when its conflict resolution was substantial. The implementing agent merges after approval; there are no system merge jobs (see `task-worktrees`).

### D5. Exit guard and `no_exit`
Drivers declare capability `exit_guard`. Codex: after `turn/completed` without an accepted exit, the worker starts a follow-up turn in the same thread ("you must end with one factory_exit_* call") up to `exit_guard_retries` (default 2). Claude Code: a Stop hook refuses to stop without an exit (`runtime-drivers`). If the turn still ends without an exit (and the adapter holds no undelivered exit), the run outcome is `no_exit`: an attempt counted against budget, and an `escalation` park with reason `no_exit` ("for review — agent did not finish") routed to the assignee's manager (before `hierarchy`: an agent with role `manager`, else the owner) with answers: `retry` (run again), `reassign`, `accept` (take the work as is: the task keeps its status and assignee and `handled_seq = wake_seq`, so it stays idle until the next trigger), `cancel`.

**Adapter delivery.** When Core is unreachable, an exit tool returns "queued — stop working" to the runtime and the adapter retries the same exit with the same idempotency key (run id) with backoff. An answer that the run is terminal (410) ends the retries. When the process ends: an exit still undelivered → outcome `lost` (not an attempt; backoff); an exit whose retry was refused with 409 (new input arrived meanwhile) → `no_exit`, and the next brief carries that input.

### D6. Authority set
`internal/core/authority.go`: `Authority(task) []Principal{who, rank}`. Members: the owner (rank 0); managers of the assignee up the chain (rank = distance from the owner); the assignee of the task that delegated this one (`created_from_task`). Until `hierarchy` lands, "managers" are the company's agents with role `manager`. Authority input kinds — the only triggers with `authority = true`: `directive` (`factory_task_direct {task, text, urgent}` / owner CLI), a comment by an authority principal, an answer to the task's decision, `ready`/run by an authority, reassign, and `artifact_approved` for a new approved version of an artifact the task requires or was pinned to. Whoever approves the version (owner or an authority) may mark the approval `urgent`, which delivers it like an urgent directive (D7).

### D7. Delivery of authority input
- **Running:** every MCP response to the task's run includes `authority_input: [{seq, author, rank, kind, text}]` for authority triggers above the adapter's `ack_seq`; exits stay blocked by D2 until seen. Several directives are all shown; on conflict the smallest depth wins (owner = 0, then the nearest manager to the owner), which the field states.
- **Parked:** in the trigger's transaction every active interruptible park with `park_seq` below it becomes `overridden` with its side effect: wait → interrupted, ask → decision superseded, block/children → overridden. Escalations `retries_exhausted`, `budget_exhausted`, `no_exit` are overridden (authority input counts as retry); `inspect` is not interruptible and needs an explicit answer. Authority input also counts as progress (clears `not_before`, `fail_streak`, attempts).
- **Urgent:** the run becomes CANCELLING with `stop_reason = directive`; the outbox publishes `run.stop {run_id}` on the per-node control subject `factory.<company>.nodes.<node_id>.control`, addressed by the run's `node_id` (the claiming worker's id until `node-agent`). This is a plain fan-out subject that every process of that node subscribes to, not the shared work-queue command stream (one durable pull consumer with MaxAckPending 1, where a stop would wait behind run commands). The heartbeat answer `cancel: true` is the guarantee (≤ one heartbeat interval). Outcome `revoked`; the next run resumes the same session and the brief starts with the directive.

### D8. Non-authority comments
`factory_task_comment` from an agent outside the authority set stores a comment and an event-feed item, and adds no trigger. Comments since the last run are listed in the next brief. An agent's comment never triggers its own task.

### D9. Agents only through MCP
The worker builds the agent's environment from an allowlist: no owner or user token, no Core admin credentials, no `factory` CLI configuration. The run token stays in the runtime environment: Codex forwards it to the MCP adapter by name and the approval shims read it there. Hiding it in a file or socket would not isolate it from an agent running as the same user (non-goal: preventing a trusted agent from bypassing MCP), and the token only acts for its own live run (410 once the run is ended, revoked or superseded), so a leak is bounded by the run. The default policy catalog denies the `factory` owner CLI on the agent's PATH (refusing shim, `node-agent` D6). Agents act on the factory only through Factory MCP tools.

## Risks / Trade-offs
- Agents may loop on 409 → the 409 body contains the input itself; the adapter raises `ack_seq` on display, so the second exit succeeds unless new input arrived.
- Exit facts come from the trusted node → acceptable in the trusted-agent model; Core still checks sequence and run identity.
- Interim authority by role until `hierarchy` → documented as temporary; one function to replace.

## Amendments

- 2026-10-20 (D7): an owner decision (approve or reject) on an approval a task requested adds the authority trigger `approval_decided` to that task (body: approval, decision, reason). It overrides the wait and ask parks only; blocks, children parks, escalations and a retry backoff stay, so a decision still does not release a lost run early. Revocation and expiry add none; tasks requiring or pinning the artifact keep `artifact_approved` (the requester gets only `approval_decided`).
- 2026-10-20 (facts): a conversation run owns no branch or PR; its adapter collects no repository facts (`FACTORY_CONVERSATION=1`) and Core neither requires nor reads them, whatever the project repository.
