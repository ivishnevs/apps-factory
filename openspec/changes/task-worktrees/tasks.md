## 1. Allocation
- [ ] 1.1 Project `repo_url`/`default_branch`; agent `spec.workspace` mode; migration drops writer indexes and adds `workspaces`. Verify: migration and config tests; two tasks of one project dispatch concurrently. Partial: project `repo_url`/`default_branch`, `RunSpec.repo_url/default_branch/workspace_mode` (from `spec.workspace.mode`, only with a repository), `workspaces` table with the worker report `POST /runs/{id}/workspace` (8e1d542, d5e9cd1). Agent `spec.workspace` field and worker wiring pending (stream B); writer indexes stay until the dispatcher change.
- [x] 1.2 `workspace.Worktrees` with per-repo serialization, idempotent ensure, lock-error retry, `.factory/` exclude and `$FACTORY_SCRATCH`. Verify: test creating 8 worktrees concurrently on one repo succeeds; a killed ensure re-runs cleanly. Done: 2499b4d; behind a disabled flag, not wired into the worker.
- [ ] 1.3 Per-worktree flock + pidfile, process group per run, kill within 30 s after exit/stop. Verify: worker test where a new run waits for the old group and seal. Partial: per-worktree flock, run pidfile with leader start time, TERM→KILL of the run group within 30 s on release, stale group killed on next acquire (behind the flag); worker integration pending.

## 2. Seal and continuity
- [x] 2.1 Local seal onto `refs/factory/wip/<task>` without touching HEAD/index; report `sealed_head`. Verify: test with staged, unstaged and untracked changes: HEAD, index and files unchanged, WIP ref contains all. Done: 2499b4d.
- [ ] 2.2 Reset to `exit_sha` after handoff/submit/done; continue as is otherwise; `fresh_from_default`. Verify: tests for each exit kind.
- [ ] 2.3 Start check and `workspace_diverged` escalation; fast-forward when the remote moved and the tree is clean; recreate a missing worktree. Verify: tests for each branch of the check. Partial: node-side check in `Worktrees.Acquire` (`TestStartCheck`): fast-forward, recreate, typed `DivergedError` that the executor ends FAILED before the runtime starts. Core's `workspace_diverged` escalation pending (stream A).

## 3. Push, scan, merge
- [ ] 3.1 Clean-tree refusal for handoff/submit/done; preflight → push → ensure PR → commit exit in the adapter. Verify: adapter test with a local bare remote and fake `gh`.
- [ ] 3.2 Pre-push hook per worktree and exit-range scan; redacted refusal. Verify: a planted test secret blocks `git push` and the exit. Partial: pre-push hook and exit-range scan verified with real gitleaks 8.30.1 (range checked first because gitleaks passes on an invalid range); redacted refusal in the adapter pending.
- [ ] 3.3 Agent merge flow: verdict back to implementer, `review_after_conflicts` policy, merge failures as attempts. Verify: real Codex smoke: implement → submit → approve → agent merges → done accepted.

## 4. Pinning and docs
- [ ] 4.1 Record node and unpushed/WIP state for pinning (enforced at claim in `node-agent`). Verify: Core stores the flag from the run end report.
- [ ] 4.2 `reset-workspace` action (seal, remove, recreate from remote, unpin, new session); `pinned_node_unavailable` reason and self-clearing escalation answered by reset-workspace. Verify: pinned node removed → reason and one escalation; reset-workspace makes the task claimable elsewhere with a fresh session. Partial (node side): `Worktrees.Reset` (seal, remove, recreate from the remote or default branch, old state on `refs/factory/reset/<task>`), `janitor.ResetWorkspace` forgets the task's sessions, `factory-node workspace reset`; the protocol action, unpinning and the escalation pending (stream A).
- [ ] 4.3 Daily unmerged-branch check for closed tasks. Verify: a cancelled task with an open PR yields exactly one event; `abandon_branch` suppresses it.
- [ ] 4.4 Update `docs/architecture.md`, `docs/task-lifecycle.md`, README limits. Verify: no remaining mention of one writer per project.
