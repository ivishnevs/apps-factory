## Why
One active writer per project serializes all work: a planner must finish before an executor starts, a reviewer waits for the executor, and two independent features of the same app can never progress together. The `workspace.Allocator` interface was introduced exactly so worktrees could replace the writer lock. Risks this change removes: tasks sharing a worktree switch branches under each other; concurrent `git worktree add` races on `.git/config`; a timed-out run leaves uncommitted work the next run can lose; a build is produced from a stale branch; and tasks are closed with their PRs never merged.

## What Changes
- A **worktree and branch per task** behind the existing allocator (`workspace.Worktrees`), replacing the project/workspace writer indexes; git operations serialized per repository and idempotent.
- **Local seal** after every run: uncommitted changes are snapshotted onto a local WIP ref without touching HEAD or the index; never pushed.
- **Continuity rules**: after `handoff`/`submit`/`done` the next run starts exactly from the handed-off commit (`exit_sha`); after `ask`/`block`/`wait` or a run without an exit, it continues from the worktree as is; a HEAD moved outside any run escalates `workspace_diverged`.
- Clean-tree rule for `handoff`/`submit`/`done`, automatic push and pull request on handoff/submit, **secret scan** before every push (pre-push hook) and at every exit.
- **The implementing agent merges after approval** (rebase, resolve conflicts, build, merge); approval is bound to the PR and `done` requires the PR merged (rule from `explicit-exits-authority`), so squash and rebase merges work; re-review after head changes by project policy. No system merge jobs; forge credentials stay on the node.
- **Node pins never deadlock**: an unavailable pinned node gives reason and escalation `pinned_node_unavailable`, answered by `reset-workspace`, which unpins. A daily check reports closed tasks with unmerged branches.
- Agent workspace modes: `task_branch` (default), `fresh_from_default` (release-type agents start from the freshly fetched default branch each run), `scratch` (no repository).

## Out of scope
Moving a task's unpushed work between machines (pushing WIP refs), cleanup scheduling and disk thresholds (`node-agent`), Core-side storage of specs/artifacts (`core-owned-project-data`).

## Dependencies
`trigger-log-scheduling`, `reliable-recovery` (outcomes, escalations), `explicit-exits-authority` (exits, exit facts, `exit_sha`).

## Capabilities
### New Capabilities
- `task-worktrees`: Per-task worktrees and branches, seal, continuity and reset rules, push/secret scan, agent-driven merge.

### Modified Capabilities
None (replaces "one writer per project" from the foundation and task-engine changes).

## Impact
`internal/workspace` (new `worktrees.go`, allocator request type), `cmd/factory-workspace` (the same allocator for the TypeScript Claude service until `runtime-drivers`), `internal/worker` (seal, reset, process group wait), `cmd/factory-mcp` (push, PR, scan at exits), `internal/storage` (drop writer indexes, `workspaces` table), `internal/core` (`workspace_diverged` escalation, pinning), `internal/tasks` (project `repo_url`, `default_branch`), `internal/config` (`spec.workspace` mode), docs `architecture.md`, `task-lifecycle.md`.
