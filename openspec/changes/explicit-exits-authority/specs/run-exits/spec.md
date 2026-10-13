## ADDED Requirements

### Requirement: Explicit exits
A run SHALL end its work with exactly one accepted MCP exit: handoff, submit, done, ask, block or wait. An accepted exit SHALL fix the run outcome `succeeded`, set `handled_seq` to the exit's `ack_seq`, and SHALL be returned unchanged when the same run retries it.

#### Scenario: Submit for review
- **WHEN** an executor calls `factory_exit_submit` with a summary
- **THEN** the task SHALL move to IN_REVIEW assigned to the reviewer with an `assignment` trigger, and the implementer SHALL be recorded

#### Scenario: Reviewer approves
- **WHEN** the reviewer calls `factory_exit_handoff` with verdict `approved`
- **THEN** the task SHALL return to the implementer with the approval recorded for the pull request and its approved head

### Requirement: Exit only from the current live run
Core SHALL refuse an exit with 410 and no state change when the calling run is not the task's current live run.

#### Scenario: Exit after urgent stop
- **WHEN** a revoked run calls `factory_exit_done`
- **THEN** Core SHALL answer 410 and the task SHALL be unchanged

### Requirement: No exit past unseen input
Core SHALL refuse an exit with 409 and no state change while the task has authority input with a sequence above the exit's `ack_seq`, and SHALL refuse `done` while any trigger is above `ack_seq`. The 409 body SHALL contain the unseen input. `ack_seq` SHALL be the highest sequence the MCP adapter actually returned to the runtime.

#### Scenario: Directive during work
- **WHEN** a manager directs a running task and the executor then calls `factory_exit_done` without having seen it
- **THEN** Core SHALL answer 409 with the directive, and a repeated exit after the adapter returned it SHALL be evaluated normally

### Requirement: Done requires a merged pull request
When a task's exit facts show a branch with commits ahead of the default branch, `done` SHALL be accepted only if its pull request is merged, unless an authority has answered `abandon_branch`.

#### Scenario: Open pull request
- **WHEN** an agent calls `factory_exit_done` while its pull request is open
- **THEN** Core SHALL refuse with a hint to rebase, resolve conflicts, build and merge first

#### Scenario: Squash merge after approval
- **WHEN** an approved pull request is squash-merged without head changes after approval and the agent calls `factory_exit_done`
- **THEN** Core SHALL accept the exit although the merged commit does not descend from the approved head

#### Scenario: Head changed with re-review required
- **WHEN** the project policy is `review_after_conflicts: required` and the PR head changed after approval without a new approval
- **THEN** Core SHALL refuse `done` and require a new submit

### Requirement: Exit guard and no_exit
Drivers SHALL ask the runtime to call an exit before ending a turn. A turn that still ends without an exit SHALL have outcome `no_exit`, count as a failed attempt, and park the task with a `no_exit` escalation to the assignee's manager (before the hierarchy exists: an agent with role `manager`, else the owner) with answers retry, reassign, accept and cancel. `accept` SHALL take the work as is and set `handled_seq` to `wake_seq`.

#### Scenario: Agent stops talking
- **WHEN** a Codex turn completes without an exit and the follow-up guard turns also end without one
- **THEN** the run outcome SHALL be `no_exit` and the manager SHALL receive "for review — agent did not finish" with retry, reassign, accept and cancel

#### Scenario: Manager accepts
- **WHEN** the manager answers `accept` to a `no_exit` escalation
- **THEN** the park SHALL clear, the task SHALL keep its status and assignee, and it SHALL not run again until a new trigger

### Requirement: Exit delivery when Core is unreachable
When Core is unreachable, an exit tool SHALL tell the runtime the exit is queued and to stop working, and the adapter SHALL retry the exit with the same key until it is accepted, refused, or Core answers that the run is terminal. A process that ends with an undelivered exit SHALL make the run `lost`; one whose retried exit was refused with 409 SHALL make it `no_exit`.

#### Scenario: Directive arrives while the exit is queued
- **WHEN** the adapter retries a queued exit after the process ended and Core refuses it with 409 because of a new directive
- **THEN** the run outcome SHALL be `no_exit` and the next brief SHALL contain the directive
