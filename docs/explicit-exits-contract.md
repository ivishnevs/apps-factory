# Explicit exits: adapter ↔ Core contract

Status: implemented on both sides behind the switch (2026-10-14): Core (§11) and adapter, worker and exit
guards (§12). The open questions of §10 were decided as written (§12). Owner decisions of 2026-10-14: explicit exits
yes, `done` only with a merged pull request yes (projects without a remote use `scratch`). Consolidates
`explicit-exits-authority` D1–D9 and `task-worktrees` D5–D6 into one contract between the MCP adapter on the node
(`cmd/factory-mcp`, `internal/factorytools`) and Core, so both sides can build against it. Nothing changes until
Core's switch is on (§9).

## 1. Who does what

| Core (stream A) | Adapter and node (stream B) |
|---|---|
| exit endpoints, 409/410 rules, idempotency, task effects per exit, `run_exits` (migration) | exit tools in the agent list (definitions in `factorytools`, served by Core), forwarding |
| `ack_seq` checks, `authority_input` in tool responses, `/agent/brief` | `ack_seq` tracking (raised only after a response reached the runtime) |
| `done` rules: merged PR, approval bound to `{pr_number, approved_head}`, `review_after_conflicts` | exit facts: git, `gh`, secret scan; clean-tree refusal; push and ensure PR before `submit`/`handoff` |
| `no_exit` outcome and escalation, `lost` for undelivered exits | exit guard (Codex follow-up turn, Claude Stop hook), exit file, stop 30 s after an accepted exit |
| authority set (interim: role `manager`), directives, park override, urgent stop | acting on the urgent stop (v2 heartbeat `stop` reason `directive`) |
| Core prompt (scheduler brief) | agent instructions in `configs/`, skills, `workers/claude` |

## 2. Exit tools (agent-visible)

All take the run's identity from the run token; none takes a task id (an exit is for the run's current task).

| Tool | Arguments | Accepted effect |
|---|---|---|
| `factory_exit_submit` | `summary` (req.), `checks: [string]`, `artifacts: [{id, version, uri}]`, `reviewer?` | IN_REVIEW; assignee := reviewer (default: the project policy, else a `manager` agent, else owner); implementer recorded; submission stored |
| `factory_exit_handoff` | `to` (req.), `note` (req.), `verdict?: approved\|changes_requested`, `feedback?`, `merge_failed?` | assignee := `to` (may be the same agent), `assignment` trigger. With `verdict` from IN_REVIEW: `approved` → implementer, IN_PROGRESS, approval bound to `{pr_number, approved_head}` from the facts; `changes_requested` (needs `feedback`) → REWORK to the implementer. `merge_failed` (the implementer of an approved task, `to` itself, no verdict): a failed merge, counted as an attempt with backoff; the third in a row → FAILED and `retries_exhausted` (task-worktrees D7) |
| `factory_exit_done` | `summary` (req.) | DONE (§6) |
| `factory_exit_ask` | `question` (req.), `options?: [string]`, `category?`, `evidence?` | `ask` park; a question to the owner (after `hierarchy`: the manager chain) |
| `factory_exit_block` | `on?: [task id]`, `on_children?: bool`, `reason` (req.) | `block` park on open tasks without a cycle, or `children` park with ≥ 1 open child; otherwise accepted as `ask` (the response says so) |
| `factory_exit_wait` | `reason`, `deadline` (RFC 3339) (req.), `wake_at` (RFC 3339) or `after` (duration) | `wait` park until the wake time, then `wait_ended` (limits and escalations: task-lifecycle.md, Waits) |

`factory_task_submit`, `factory_task_accept` and `factory_task_reject` leave the agent list when the switch is on;
the owner's `factory tasks review` maps to a verdict handoff. Managers get `factory_task_direct {task, text, urgent?}`
(§7). Every accepted exit sets the run outcome `succeeded` and `handled_seq = ack_seq`.

## 3. Endpoints (run-token gateway, like `/agent/call`)

`POST /agent/exit/preflight` and `POST /agent/exit`, body:

```json
{"kind": "submit", "arguments": {"summary": "…", "checks": ["go test ./..."]}, "ack_seq": 17,
 "facts": {"head": "9f3c…", "dirty": false, "branch": "af/t1-login", "default_branch": "main",
           "ahead_of_default": 3, "pushed": true,
           "pr": {"number": 42, "state": "open", "head_sha": "9f3c…", "url": "https://…/pull/42"},
           "scan": {"ok": true}}}
```

- `facts` is required for `submit`, `handoff`, `done`; optional for `ask`, `block`, `wait`. Without a repository
  (scratch mode) `head`/`branch` are empty and `pr` is null. `pr.state`: `open|closed|merged`. Core never reads
  the disk; facts are trusted node data, Core checks run identity and sequences.
- **preflight** applies every check below without changing state; the adapter calls it before pushing.
- **exit** applies the checks and the effect in one transaction. Idempotent by run id: the same kind and
  arguments return the stored result; another kind after an accepted exit is `409 exit_already_accepted`.

Answers (always JSON):

| Status | Body | Adapter does |
|---|---|---|
| 200 | `{"exit": "submit", "task": {"id", "status", "assignee", "version"}, "ack_seq": 17}` | tells the agent the exit is accepted and to finish now; writes the exit file (§5) |
| 409 | `{"reason": "unseen_input", "unseen": [{"seq", "kind", "author", "authority", "text"}], "hint": "…"}` | returns `unseen` to the runtime as the tool result, then raises `ack_seq`; the agent may exit again |
| 409 | `{"reason": "dirty_tree\|pr_not_merged\|needs_approval\|secret_found\|exit_already_accepted\|invalid_exit", "hint": "…"}` | returns the hint; nothing is pushed or committed |
| 410 | `{"reason": "not_current_run"}` | tells the agent to stop; ends delivery retries |
| 5xx / network | — | "queued — stop working" to the runtime; retries the same exit with backoff (§5) |

Checks, in order: 410 when the run is not the task's current live run (revoked, ended, superseded);
`invalid_exit` for bad arguments or a block that cannot apply; `unseen_input` when an authority trigger is above
`ack_seq` (for `done` also any trigger above it); `dirty_tree` for `submit`/`handoff`/`done` with `facts.dirty`;
`secret_found` with `facts.scan.ok == false` (the adapter's scan; Core records a report item); for `done`
§6.

## 4. Sequences and `ack_seq`

- `GET /agent/brief` → `{"ack_seq": brief_to, "task_id": "…"}`: the adapter's starting `ack_seq` (the run's brief
  window end; v1 and v2 alike).
- Every `/agent/call` and `/agent/exit*` request carries header `X-Factory-Ack-Seq: N`. When the task has authority
  triggers above N, Core wraps the tool result: `{"result": <tool result>, "authority_input": [{"seq", "author",
  "rank", "kind", "text"}], "winning": seq}` and sets response header `X-Factory-Seen-Seq: M` (the highest seq
  shown). On conflicting directives the smallest rank wins (owner = 0); `winning` names it.
- The adapter raises `ack_seq` to `M` (or to a 409's highest `unseen` seq) only after the response line was
  written to the runtime. A response that never reached the runtime never counts as seen.

## 5. Delivery, exit file and outcomes

- The worker sets `FACTORY_EXIT_FILE` (inside the run's shim directory) for the adapter. The adapter writes
  `{"state": "accepted|pending|refused", "kind": "…", "at": "…"}` there atomically on each change: `pending` while an
  exit is queued for retry, `accepted` on 200, `refused` when a retried exit got 409 (new input arrived meanwhile).
- After `accepted` the worker lets the turn finish and stops the runtime if it still runs 30 s later; work done
  after the exit is not part of it (sealed on the WIP ref, `task-worktrees`).
- The terminal event carries the file's state: `Event.exit_state: "accepted|pending|refused|none"` (additive
  field). Core: a run with an accepted exit (`run_exits`) is `succeeded` whatever the worker reported; `succeeded`
  without one is `no_exit` (`refused` or `none`) or `lost` (`pending`: the exit may be delivered later, the retry is
  safe).
- Exit guard (stream B, `runtime-drivers` 2.4): Codex starts a follow-up turn "end with one factory_exit_* call"
  up to `exit_guard_retries` (default 2); Claude's Stop hook refuses to stop without an exit.
- `no_exit` (Core): an attempt counted against the budget; an escalation `no_exit` ("agent did not finish") to
  the assignee's manager (`reports_to`), else the owner, answers `retry`, `reassign` (to the answering manager; the
  owner's leaves it unassigned), `accept` (keep status and assignee, `handled_seq = wake_seq`), `cancel`; any
  authority input clears it.

## 6. `done`

Refused with `409 pr_not_merged` (hint: merge your PR first: rebase on the default branch, resolve conflicts,
build, merge) when `facts.ahead_of_default > 0` and `facts.pr.state != "merged"`, unless an authority answered
`abandon_branch` for the task. When the project requires review, `done` also needs an approval bound to
`facts.pr.number`; with `review_after_conflicts: required` and `facts.pr.head_sha` different from the approved
head, `409 needs_approval` (submit again before merging). Squash and rebase merges pass (no ancestry check).
Scratch projects (no remote) have no PR: `done` is accepted with a clean tree.

## 7. Authority input

- Authority set (`hasAuthority` in `internal/core/stalls.go`, `hasAuthorityTx` in storage): the owner (rank 0), the
  managers up the assignee's `reports_to` chain (rank = depth, 1 reports to the owner), the assignee of the
  delegating task (`origin_task`). Authority triggers: directives (`factory_task_direct`,
  `factory direct TASK TEXT [--urgent]`), comments by authority principals, answers to the task's questions,
  `ready`/reassign by an authority, `artifact_approved` for required artifacts and for tasks pinned to another
  version of the artifact (they require it), and `approval_decided` on the task that requested an approval
  (`approval.task_id`) when the owner approves or rejects it (action, command or artifact approval; the body names
  the approval, the decision and its reason; revocation and expiry add none). `approval_decided` overrides only
  the wait and ask parks: blocks, children, escalations and a retry backoff stay. Non-authority comments
  add no trigger; they are listed in the next brief.
- Parked task: an authority trigger overrides interruptible parks (wait, ask, block, children) and the
  escalations `retries_exhausted`, `budget_exhausted`, `no_exit` (it counts as retry); `inspect` needs its answer.
- Urgent: the run becomes CANCELLING with `stop_reason = directive`; v1: `cancel: true` in the heartbeat answer,
  v2: `run.stop` on `factory.<company>.nodes.<node_id>.control` at once (plain NATS, outside the command stream;
  Core's outbox publishes it immediately) and the heartbeat `stop` `revoked` as the guarantee. An urgent artifact approval
  (`factory approve ID REASON --urgent`) stops the runs of the tasks it wakes the same way. Outcome `revoked`; the next
  run resumes the session and the brief starts with the directive.

## 8. Facts collection (adapter, `task-worktrees` D5–D6)

In the run's working directory: `head = git rev-parse HEAD`; `dirty` = tracked changes or untracked non-ignored
files (`git status --porcelain`); `branch`; `ahead_of_default = git rev-list --count origin/<default>..HEAD`;
`pushed` = `origin/<branch>` equals `head` after a fetch; `pr` from `gh pr view <branch> --json
number,state,headRefOid,url` (null when none); `scan` = the configured scanner over
`merge-base(default, HEAD)..HEAD`. For `submit`/`handoff` the adapter runs preflight, then pushes the branch
(pre-push hook scans too) and ensures a PR (`gh pr create` when none), collects facts again and sends the exit.
A forge or Core failure retries with the same run key.

## 9. Switch and rollout

- Core switch `FACTORY_EXPLICIT_EXITS=1` (per Core until companies have settings). Off: today's tools and implicit
  submission (any `succeeded` turn → IN_REVIEW). On: exit tools listed, old submit/accept/reject removed, a
  `succeeded` turn without an exit → `no_exit`.
- Order: Core endpoints and `run_exits` behind the switch → adapter (tools, `ack_seq`, facts, delivery, exit file)
  → exit guard in both drivers → prompts and instructions (Core brief; agent YAML and skills by stream B) → switch
  on, then the E2E (stage 9) runs on the new rules.

## 10. Open questions for review

1. `GET /agent/brief` vs passing `brief.to` in the run environment (`FACTORY_ACK_SEQ`): the endpoint works for v1
   and v2 alike; is one call at adapter start acceptable?
2. Wrapping tool results with `authority_input` changes the result shape only while input is unseen; acceptable
   for both runtimes, or always wrap?
3. The adapter pushes and creates the PR before `submit`/`handoff`: forge credentials stay on the node; is `gh`
   the only forge for now?
4. Exit file path: inside the shim directory (exists only with guards) or a per-run file next to the run's
   pidfile in `state_dir`?

## 11. Core implementation notes

Implemented (`internal/storage/exits.go`, `internal/core/exits.go`, migration 0019), off unless
`FACTORY_EXPLICIT_EXITS=1`:

- Exits through `/agent/exit` (with facts) and through `/agent/call` (an adapter that does not intercept exit
  tools yet; no facts, `ack_seq` from the header or the brief end). Without facts a task of a project with a
  repository is refused `409 facts_required` for submit, handoff and done; scratch projects work today. A
  conversation run owns no branch or PR: its adapter (`FACTORY_CONVERSATION=1`) collects no facts and Core
  requires (and reads) none, whatever the project's repository.
- `/agent/exit*` on a stopped run answers 410 (the credential is checked first, then the run's state).
- Default reviewer is the owner (`factory tasks review`, mapped to a verdict: approve returns a repository task
  to its implementer to merge, otherwise ends it; reject returns it with the feedback). A `reviewer` agent gets
  its own review run while the task stays IN_REVIEW. The manager as default reviewer arrives with the hierarchy.
- `block on` adds the named tasks as dependencies (the scheduler and the `stalled` escalation already handle
  them); `on_children` blocks until the last child closes; impossible blocks become an ask. `wait` sets
  `not_before` and a wait park that the tick turns into a `wake` trigger; `deadline` is stored, not enforced yet.
- Authority set: see §7 (the hierarchy replaced role `manager`). A manager's directive does not lift an owner's
  manual block.
- An urgent directive revokes the running run (CANCELLING; v1 cancel, v2 stop `revoked`); its `revoked` end
  returns the task to READY and the next brief starts with the directive.
- Since done: `abandon_branch`, `review_after_conflicts`, `artifact_approved` as authority input, the park
  override for every authority trigger, depth ranks. Not yet: the secret-scan report item.

## 12. Adapter, worker and exit guard notes

- Adapter (`internal/factorytools/adapter.go`, `cmd/factory-mcp`): `ack_seq` starts at `GET /agent/brief` (an
  older Core without it leaves sequences untracked: no header); `X-Factory-Ack-Seq` on every request; the seq of
  `X-Factory-Seen-Seq` and of a 409's `unseen` counts only after the response line was written to the runtime
  (`ServeWith`'s delivered hook). Exit tools are intercepted: facts from `FACTORY_WORKDIR` (`git rev-parse`,
  `status --porcelain`, `fetch`, `rev-list --count origin/<default>..HEAD`, `gh pr view`), the clean-tree and
  scan refusals on the node, preflight → `git push -u origin HEAD:refs/heads/<branch>` → `gh pr create --fill`
  for submit and handoff, then the exit. Core unreachable: "queued — stop working", retries with the same body
  (2 s doubling to 1 min), a second exit meanwhile is refused; the process waits for the delivery at exit.
- Worker: `FACTORY_EXIT_FILE` (a per-run temp dir), `FACTORY_WORKDIR`, `FACTORY_DEFAULT_BRANCH` and, in a worktree,
  `FACTORY_SCAN_COMMAND` (JSON array, the node's scanner) reach factory-mcp; the terminal event carries
  `exit_state`; a runtime still running 30 s (`ExitGrace`) after an accepted exit is stopped (the exit stands).
- Exit guard, on when the run credential says `explicit_exits`: Codex starts a follow-up turn with
  `driver.ExitGuardPrompt` in the same thread, Claude's shim blocks the Stop hook with the same text; both at most
  `ExitGuardRetries` (2) times, and both stop as soon as the exit file says accepted or pending.
- §10 decided: one `/agent/brief` call at adapter start; results are wrapped only while input is unseen; `gh` is the
  only forge; the exit file lives in its own temp dir (not the shim dir, which exists only with guards).
