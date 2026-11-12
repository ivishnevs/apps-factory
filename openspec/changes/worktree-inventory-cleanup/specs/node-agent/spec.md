## MODIFIED Requirements

### Requirement: Janitor and disk thresholds
The node SHALL remove worktrees of closed tasks after sealing, SHALL run the company's cleanup hooks, SHALL prune caches by age, and SHALL emit one low-disk alert and clean aggressively below the low threshold. Below the critical threshold the node SHALL stop claiming work and review runs but SHALL keep claiming conversation runs while free space stays above `disk.conversation_floor_gib` (default 1 GiB); an owner pause SHALL still stop every claim. Which worktrees to remove SHALL be decided from the worktree inventory the node reports, not from which node Core last recorded for the task.

#### Scenario: Disk falls below the low threshold
- **WHEN** free disk space drops below the low watermark
- **THEN** the node SHALL prune caches of closed tasks immediately and Core SHALL emit exactly one `node.low_disk` attention event until space recovers

#### Scenario: Owner writes during a disk pause
- **WHEN** the node has 3.6 GiB free with a critical watermark of 5 GiB and the owner writes to the CEO
- **THEN** the node SHALL claim the CEO's conversation run and SHALL NOT claim any work or review run

#### Scenario: Disk nearly full
- **WHEN** free space falls below `disk.conversation_floor_gib`
- **THEN** the node SHALL claim no run of any kind until space recovers

## ADDED Requirements

### Requirement: Worktree inventory drives cleanup
A node with worktrees enabled SHALL report its task worktrees (task, project, sealed head, unpushed work, last use, size, busy) to Core, and Core SHALL answer a removal for every reported worktree of a DONE or CANCELLED task, of a task unknown to Core for 24 h, or of an open task whose workspace another node owns when the copy has no unpushed work and was unused for 24 h or the node's disk is low. Core SHALL repeat a removal while the node reports the worktree and SHALL NOT limit how many times it is answered.

#### Scenario: Task moved to another node before closing
- **WHEN** a task ran on node A, its review fix ran on node B (the workspace row now names B), and the task becomes DONE
- **THEN** Core SHALL answer the removal to both A and B, and both worktrees SHALL be gone after their next idle heartbeat

#### Scenario: Worktree busy when the task closes
- **WHEN** a closed task's worktree stays locked by a hook for 40 minutes
- **THEN** Core SHALL keep answering the removal and the node SHALL remove the worktree once the lock is free

#### Scenario: Node offline when the task closes
- **WHEN** a node comes back two days after a task it holds was closed
- **THEN** its first inventory heartbeat SHALL lead to the worktree's removal

#### Scenario: Unpushed work on a node that does not own the workspace
- **WHEN** a node reports a worktree with unpushed commits for an open task whose workspace another node owns
- **THEN** Core SHALL NOT answer a removal and SHALL raise one `node.worktree_stuck` attention event with reason `unpushed_elsewhere`

#### Scenario: Removal does not finish
- **WHEN** a removal has been answered for 1 h and the node still reports the worktree
- **THEN** Core SHALL raise one `node.worktree_stuck` attention event with the node's reported reason and SHALL clear it when the worktree is gone
