## ADDED Requirements

### Requirement: Tied work offered first
When a node polls for work, Core SHALL offer it first the runs pinned to that node (unpushed work in its worktree),
then the runs whose agent session it holds, then the other claimable runs in creation order. The order SHALL NOT make
claimable any run that the existing checks refuse.

#### Scenario: Pinned run before older work
- **WHEN** a full node holding VP-3's unpushed work frees a slot while two older unpinned runs are queued
- **THEN** the node SHALL be offered VP-3 first, and the older runs SHALL stay available to other nodes

### Requirement: Tied waits name their node
A task pinned to an online node without a free slot SHALL wait as `pinned_node` naming that node and the runs filling
it, and the Nodes page SHALL list per node the pinned tasks and held sessions with the runs waiting for them.

#### Scenario: Pinned to a full node
- **WHEN** VP-3 is pinned to `hetzner-hel1`, which runs 4 of 4, and the Mac has free slots
- **THEN** the task SHALL show that it waits for `hetzner-hel1` because its unpushed work is there, not that all nodes
  are busy
