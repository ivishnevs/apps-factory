## 1. Allocation
- [ ] 1.1 Project `repo_url`/`default_branch`; agent `spec.workspace` mode; migration drops writer indexes and adds `workspaces`. Verify: migration and config tests; two tasks of one project dispatch concurrently.
- [ ] 1.2 `workspace.Worktrees` with per-repo serialization, idempotent ensure, lock-error retry, `.factory/` exclude and `$FACTORY_SCRATCH`. Verify: test creating 8 worktrees concurrently on one repo succeeds; a killed ensure re-runs cleanly.
- [ ] 1.3 Per-worktree flock + pidfile, process group per run, kill within 30 s after exit/stop. Verify: worker test where a new run waits for the old group and seal.

## 2. Seal and continuity
- [ ] 2.1 Local seal onto `refs/factory/wip/<task>` without touching HEAD/index; report `sealed_head`. Verify: test with staged, unstaged and untracked changes: HEAD, index and files unchanged, WIP ref contains all.
- [ ] 2.2 Reset to `exit_sha` after handoff/submit/done; continue as is otherwise; `fresh_from_default`. Verify: tests for each exit kind.
- [ ] 2.3 Start check and `workspace_diverged` escalation; fast-forward when the remote moved and the tree is clean; recreate a missing worktree. Verify: tests for each branch of the check.

## 3. Push, scan, merge
- [ ] 3.1 Clean-tree refusal for handoff/submit/done; preflight → push → ensure PR → commit exit in the adapter. Verify: adapter test with a local bare remote and fake `gh`.
- [ ] 3.2 Pre-push hook per worktree and exit-range scan; redacted refusal. Verify: a planted test secret blocks `git push` and the exit.
- [ ] 3.3 Agent merge flow: verdict back to implementer, `review_after_conflicts` policy, merge failures as attempts. Verify: real Codex smoke: implement → submit → approve → agent merges → done accepted.

## 4. Pinning and docs
- [ ] 4.1 Record node and unpushed/WIP state for pinning (enforced at claim in `node-agent`). Verify: Core stores the flag from the run end report.
- [ ] 4.2 `reset-workspace` action (seal, remove, recreate from remote, unpin, new session); `pinned_node_unavailable` reason and self-clearing escalation answered by reset-workspace. Verify: pinned node removed → reason and one escalation; reset-workspace makes the task claimable elsewhere with a fresh session.
- [ ] 4.3 Daily unmerged-branch check for closed tasks. Verify: a cancelled task with an open PR yields exactly one event; `abandon_branch` suppresses it.
- [ ] 4.4 Update `docs/architecture.md`, `docs/task-lifecycle.md`, README limits. Verify: no remaining mention of one writer per project.
