## ADDED Requirements

### Requirement: Worktree and branch per task
Each task with repository work SHALL run in its own worktree on its own branch, allocated through the workspace allocator. Git operations on one repository SHALL be serialized and idempotent, and tasks of the same project SHALL be able to run concurrently.

#### Scenario: Two tasks in one project
- **WHEN** two runnable tasks of the same project are assigned to different agents
- **THEN** both SHALL run at the same time in separate worktrees on separate branches

#### Scenario: Concurrent worktree creation
- **WHEN** several worktrees of one repository are created at the same time
- **THEN** all SHALL be created without a git lock failure being reported as a task failure

### Requirement: Local seal
After every terminal run the worker SHALL snapshot uncommitted changes onto a local WIP ref without changing HEAD or the index, SHALL NOT push it, and SHALL report the sealed head to Core.

#### Scenario: Timeout with uncommitted work
- **WHEN** a run times out with modified and untracked files
- **THEN** the files SHALL remain in the worktree, a WIP ref SHALL contain them, and the next run SHALL continue from the worktree as it is

### Requirement: Start from the handed-off commit
After an accepted handoff, submit or done, the next run of the task SHALL start from the exit commit. After ask, block, wait or a run without an exit, it SHALL continue from the worktree as it is. A HEAD changed outside any run SHALL create a `workspace_diverged` escalation.

#### Scenario: Reviewer receives a handoff
- **WHEN** the executor submits at commit C and then writes more files before its process ends
- **THEN** the reviewer's run SHALL start with HEAD at C and a clean tree, and the later files SHALL exist only in the WIP ref

### Requirement: Clean tree and secret scan before pushes
Handoff, submit and done SHALL be refused while the worktree is dirty. Every push from a worktree and every exit SHALL be scanned for secrets, and a finding SHALL refuse the push or exit.

#### Scenario: Secret in a commit
- **WHEN** an agent commits a credential and calls `factory_exit_submit`
- **THEN** the exit SHALL be refused with a redacted finding and nothing SHALL be pushed

### Requirement: Agent merges after approval
After a reviewer's approval the implementing agent SHALL merge its own pull request, and the task SHALL become DONE only with the pull request merged. The system SHALL NOT run merge jobs. A failed merge SHALL follow the ordinary attempt and escalation rules.

#### Scenario: Merge conflict after approval
- **WHEN** the implementer cannot merge because of conflicts three times in a row
- **THEN** the task SHALL be escalated to the implementer's manager with `retries_exhausted`

### Requirement: Pinned node unavailable
A task pinned to a node that is offline beyond the pin timeout, removed, or unable to run the current assignee SHALL report reason `pinned_node_unavailable` and SHALL get one escalation whose answers include `reset-workspace`. `reset-workspace` SHALL remove the pin, recreate the worktree from the remote branch and start a new session.

#### Scenario: Pinned machine replaced
- **WHEN** a task's pinned node is removed and the owner answers `reset-workspace`
- **THEN** the task SHALL become claimable on any capable node and its next run SHALL start from the remote branch in a new session

### Requirement: Unmerged branches of closed tasks reported
Core SHALL periodically report closed tasks whose branch is ahead of the default branch with an unmerged pull request and no `abandon_branch` answer.

#### Scenario: Cancelled task with an open PR
- **WHEN** a task is cancelled while its pull request is open
- **THEN** the next daily check SHALL emit one event for it to the manager of its last assignee
