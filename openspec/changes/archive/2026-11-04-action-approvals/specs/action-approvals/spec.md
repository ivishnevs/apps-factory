Out of scope: artifact version approvals and external protected executors are outside this change.

## ADDED Requirements

### Requirement: Approval bound to an exact mutation
Core SHALL bind each approval to an exact supported mutation, agent, project and task. Parameter changes SHALL require a new approval; exact logical retries SHALL remain idempotent.

#### Scenario: Changed parameters
- **WHEN** an agent repeats an approved call with different arguments
- **THEN** Core SHALL require a new approval, while repeating the exact approved call SHALL replay its result

### Requirement: Owner-only decisions
Only the owner SHALL approve, reject or revoke an approval, using the approval's version and an idempotency key. Ordinary inbox answers SHALL NOT resolve approvals.

#### Scenario: Inbox answer to an approval
- **WHEN** an approval is answered through an ordinary inbox answer
- **THEN** the approval SHALL stay pending

### Requirement: Deny precedence and fail closed
Deny and capability restrictions SHALL take precedence over any grant. Pending, rejected, revoked and expired intents SHALL fail closed with 423.

#### Scenario: Denied call with a grant
- **WHEN** a call is denied by policy although an approval grant exists for it
- **THEN** Core SHALL refuse the call

#### Scenario: Pending approval
- **WHEN** an agent calls a mutation whose approval is pending, rejected, revoked or expired
- **THEN** Core SHALL answer 423 without performing the mutation

### Requirement: Blocking requests release the writer
Blocking requests SHALL release the writer through normal runtime completion and SHALL resume the same task and thread after the decision; UNKNOWN SHALL never resume automatically.

#### Scenario: Approved after the run ended
- **WHEN** a task's run ends normally while waiting for approval and the owner approves
- **THEN** the same task and thread SHALL resume in a new run

#### Scenario: Run outcome unknown
- **WHEN** the waiting run's outcome is UNKNOWN and the owner approves
- **THEN** the task SHALL NOT resume automatically

### Requirement: Atomic persistence
Requests, decisions, inbox answers and task state transitions SHALL persist atomically.

#### Scenario: Restart after a decision
- **WHEN** Core restarts right after an owner decision
- **THEN** the decision, the inbox entry and the task transition SHALL all be present or all absent
