## Context
`workspace.Local.Acquire(ctx, path)` takes an exclusive flock on `<workspace>/.factory-writer.lock`; Core enforces `project_writer` and `workspace_writer` unique indexes over live and UNKNOWN runs. Projects have one immutable canonical workspace path. After `explicit-exits-authority` every accepted exit carries git facts and `exit_sha` collected by the MCP adapter.

## Goals / Non-Goals
Goals: parallel work across tasks of one project; no lost uncommitted work; every assignee starts from exactly what was handed over; secrets never pushed; merged code for every done task.
Non-goals: cross-machine relocation of unpushed work, merge queues, system-run merges.

## Decisions

### D1. Allocation
`Allocator.Acquire(ctx, Request{Company, Project, RepoURL, DefaultBranch, Task, Mode, Branch, ExitSHA, SealedHead, Reset bool}) (Allocation, error)`; `Local` stays for `scratch` mode and tests. `Worktrees{Root}` lays out `<root>/<company>/<project>/repo` (one clone, fetched under a per-repo mutex + flock) and `<root>/<company>/<project>/tasks/<task-id>` (one worktree). Branch `af/<task-id>-<slug>` from the default branch on first use. All git mutations of one repository are serialized; "ensure worktree" is idempotent after a crash; git lock errors (`index.lock`, `config.lock`, `packed-refs.lock`) are retried with backoff, never reported as `setup_failed`. Each worktree gets `.factory/` (state, lock, pidfile) ignored by git in that worktree only (per-worktree `core.excludesFile`, see `per-worktree-git-excludes`) and a scratch directory `$FACTORY_SCRATCH` outside the worktree. `project_writer` and `workspace_writer` indexes are dropped; `active_task_run` and `session_turn` remain. Core keeps metadata only: `workspaces(company_id, task_id, mode, branch, node_id, exit_sha, sealed_head, sealed_at)`, reported by the worker.

### D2. Run exclusivity on a worktree
Per-worktree flock plus a pidfile with process start time. A new run on a locked worktree waits until the previous run's process group is gone and its seal is recorded. Each run has its own process group, killed within 30 s after an accepted exit or a stop.

### D3. Local seal
After every terminal run (and on worker start for runs of an older epoch): stop the process group; if the worktree has changes, snapshot them with a temporary index (`GIT_INDEX_FILE`, `git add -A`, `write-tree`, `commit-tree -p HEAD`) onto the **local** ref `refs/factory/wip/<task-id>` — HEAD and the real index are untouched, nothing is pushed; write `sealed_head = HEAD` to `.factory/state`; report `{sealed_head, wip_sha}` with the run end; Core sets the run's `sealed` flag. WIP refs are kept 14 days after the task closes.

### D4. Before the next run
- Last accepted exit was `handoff`, `submit` or `done` → `git reset --hard <exit_sha>` + `git clean -fd`, `sealed_head = exit_sha` (anything written after the exit survives only in the WIP ref).
- Last outcome was `ask`, `block`, `wait`, `no_exit`, an attempt outcome, `lost` or `revoked` → continue from the worktree as it is.
- Start check: HEAD == `sealed_head` → start (rebases by the agent inside runs are fine); remote branch moved and worktree clean → fast-forward; otherwise HEAD changed outside any run → `workspace_diverged` escalation (answers: reset from the remote branch, cancel). A missing worktree is recreated from the remote branch (or the default branch) without the check.
- `fresh_from_default`: fetch and reset to `origin/<default>` at every run (stable worktree path, no feature branch).
- `reset-workspace` (authority action, owner CLI): seal, remove the worktree (the WIP ref is kept), recreate it from the remote branch (or the default branch) on whichever node claims next, clear the node pin (D8), and start a new runtime session (`reliable-recovery` D3).

### D5. Clean tree, push and pull request
`handoff`, `submit` and `done` are refused while the tree is dirty (409: commit, discard, or move files to `$FACTORY_SCRATCH`); `ask`, `block` and `wait` accept a dirty tree. On `handoff`/`submit` the adapter first runs Core's preflight (409/410, nothing committed), then pushes a clean unpushed branch and ensures a pull request (`gh pr create` when none), then commits the exit; if the forge is unreachable the adapter retries the exit with the same run key.

### D6. Secret scan
Every worktree uses a pre-push hook (`extensions.worktreeConfig` + `core.hooksPath` per worktree) that runs the configured scanner (default gitleaks) so agents' own pushes are scanned; the adapter also scans `merge-base(default, HEAD)..HEAD` at every exit. A finding refuses the exit with a redacted report and creates a report item; it is an ordinary refusal, not an attempt. Default scanner (verified with gitleaks 8.30.1): `gitleaks git --redact --no-banner --no-color --verbose --exit-code 1 --log-opts <range>`; `--verbose` puts the redacted findings (rule, file, line, commit) in the report. gitleaks exits 0 on a bad range or outside a repository, so the adapter resolves the range with `git rev-list` before scanning.

### D7. Merge by the implementing agent
Reviewer `approved` verdict returns the task to the implementer with the approval bound to the PR (`pr_number`, `approved_head`; `explicit-exits-authority` D4). The implementer updates from the default branch, resolves conflicts, builds, and merges (`gh pr merge`, any merge method), then calls `done`, which is accepted only with `pr.state == merged`. Project policy `review_after_conflicts: agent_decides | required` (default `agent_decides`): with `required`, a PR head that changed after approval needs a new `submit` and approval before merging (Core refuses `done` otherwise); with `agent_decides`, the implementer re-submits when its conflict resolution was substantial. A failed merge is an ordinary attempt: the implementer reports it with `factory_exit_handoff` to itself carrying `merge_failed: "<why>"` (checks, branch protection, conflicts). Core accepts `merge_failed` only from the task's implementer, only on a task with a recorded approval, and only without a `verdict` (any other caller or shape is refused `invalid_exit`). It counts one attempt with the ordinary backoff (1, 5, 15 minutes before the next run) and records `merge failed: <why>` as the task's result; the third failed merge in a row makes the task FAILED with one `retries_exhausted` escalation to the implementer's manager. Forge credentials live on the node and are used by the agent; Core holds none.

### D8. Node pinning
A task whose worktree on node N has unpushed commits or a WIP ref newer than the remote branch is claimable only on N; otherwise any capable node may claim it and recreates the worktree from the remote branch. (Claim-side enforcement arrives with `node-agent`; until then there is one worker host.) If N is offline longer than `pin_timeout` (default 30 min), was removed, or lacks the labels or driver of the current assignee, the task's reason is `pinned_node_unavailable` and Core opens one `pinned_node_unavailable` escalation (answers: `reset-workspace` — continue from the remote branch on any node, unpushed work stays in N's WIP ref; `wait`; `cancel`). It clears itself when N can claim again. `reset-workspace` always removes the pin.

### D9. Unmerged branch check
A daily reconciler check lists tasks closed (DONE or CANCELLED) in the last 30 days whose last exit facts show a branch ahead of the default branch with a PR not merged and no `abandon_branch` answer; each produces one feed event to the manager of the last assignee.

## Risks / Trade-offs
- More disk per project → cleanup of closed tasks' worktrees after seal and cache pruning in `node-agent`.
- Agents committing junk to satisfy the clean-tree rule → guidance in the refusal; `$FACTORY_SCRATCH` for notes and temporary files.
- Scanner false positives block exits → the refusal names the file/line; an allowlist file in the repository is respected.

## Amendments

- 2026-10-20: conversation runs get no worktree or branch and take no writer slot; they run in their own conversation directory on the node (`node-agent` amendment, bc070e8).
