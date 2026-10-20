## ADDED Requirements

### Requirement: Immutable artifact versions
Artifact id, version and URI SHALL be immutable within a project and SHALL attach to producer provenance.

#### Scenario: URI mismatch
- **WHEN** a task references a registered artifact version with a different URI
- **THEN** Core SHALL fail closed

### Requirement: Version-bound owner approval
Owner approval SHALL bind the exact artifact version and SHALL remain separate from task review. Approval of v1 SHALL NOT satisfy a v2 requirement. Artifact decisions SHALL NOT authorize Factory action calls.

#### Scenario: New version after approval
- **WHEN** v1 is approved and a task requires v2
- **THEN** the task SHALL wait for approval of v2

#### Scenario: Task accepted in review
- **WHEN** a task producing an artifact is accepted in review
- **THEN** the artifact version SHALL remain unapproved

### Requirement: Tasks requiring approved versions
Tasks MAY require approved versions before execution; arbitrary roles and ordering SHALL work. Dispatch and claim SHALL validate pinned artifact approvals, and revoke SHALL stop unclaimed work. Core SHALL retain immutable approval inputs for already started runs; revoke SHALL NOT undo work.

#### Scenario: Revoke before claim
- **WHEN** the owner revokes an approval that a dispatched but unclaimed run pinned
- **THEN** the claim SHALL be refused and the task SHALL wait again

#### Scenario: Revoke after start
- **WHEN** the owner revokes an approval after a run started with it
- **THEN** the run SHALL keep its approval inputs and its work SHALL NOT be undone

### Requirement: Durable inbox and waiters
Core SHALL persist inbox entries, decisions and eligible waiters atomically and SHALL survive restart.

#### Scenario: Restart while waiting
- **WHEN** Core restarts while a task waits for an artifact approval
- **THEN** the inbox entry and the waiting task SHALL remain and an approval SHALL release the task

### Requirement: Checks on implicit registration
Deny and capability checks SHALL apply to implicit registration through task submission. Manual blocks and UNKNOWN quarantine SHALL NOT be cleared by artifact decisions.

#### Scenario: Approval of a blocked task's input
- **WHEN** the owner approves an artifact required by a manually blocked or quarantined task
- **THEN** the task SHALL stay blocked or quarantined
