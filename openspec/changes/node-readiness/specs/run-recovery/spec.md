## ADDED Requirements

### Requirement: Node environment failures charge the node
A run that ends `setup_failed` for a node-side cause (repository access, worktree, runtime binary, missing required
tool) SHALL NOT count toward its task's retries. Core SHALL record a readiness fault for that node and project, SHALL
offer the project's runs only to nodes without such a fault that meet the project's declared requirements, and SHALL
let the task wait with reason `no_ready_node` when no node is ready, escalating only with the nodes' faults and fixes.

#### Scenario: New node without repository access
- **WHEN** a run of task T fails on node N with "Repository not found" while node M is ready
- **THEN** T's retries SHALL be unchanged, T SHALL be offered to M, and N SHALL show the fault and get no runs of the
  project until its check passes

#### Scenario: A stale check does not clear a run's fault
- **WHEN** node N reports a passing check made before its run of the project failed
- **THEN** the fault SHALL stay until a check newer than the failed run passes

#### Scenario: No ready node
- **WHEN** every node with the agent's runtime has a readiness fault for the project
- **THEN** T SHALL wait with reason `no_ready_node` naming the nodes and their fixes and SHALL NOT escalate as
  `retries_exhausted`

### Requirement: Pausing a node asks about its sessions
Pausing a node that holds agent sessions SHALL show the sessions and the runs waiting for them and SHALL either
keep them until the release timeout or release them at once; a queued run waiting for an inactive holder SHALL be
releasable on its own.

#### Scenario: Release now
- **WHEN** the owner releases the session of a run waiting for a paused node
- **THEN** another node SHALL take the run with a fresh session, at every later look, until the holder is active again
