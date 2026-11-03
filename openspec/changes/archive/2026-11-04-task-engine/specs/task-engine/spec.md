# Task Engine requirements

## ADDED Requirements

### Requirement: Tasks
Core SHALL validate lifecycle actions, same-project DAG dependencies and optimistic versions.
Dependencies SHALL only unblock scheduling after DONE, not runtime completion or submission.
Duplicate mutation IDs SHALL replay the original response without repeated effects.

#### Scenario: Dependency submitted
- **WHEN** a task's dependency is submitted to IN_REVIEW
- **THEN** the task SHALL stay unschedulable until the dependency is DONE

### Requirement: Scheduling
Dispatch SHALL atomically persist task IN_PROGRESS, a unique attempt/run and outbox command.
Scheduler SHALL use priority and availability without fixed role sequences. Restart SHALL recover
from persisted state. Unknown outcomes SHALL quarantine the workspace and block automatic retry.

#### Scenario: Unknown outcome
- **WHEN** a run's outcome is unknown
- **THEN** the workspace SHALL be quarantined and the task SHALL NOT be retried automatically

### Requirement: Review
Completed runs SHALL submit to IN_REVIEW; owner/authorized reviewer SHALL explicitly accept/reject.
Rejection SHALL preserve feedback and allow a new attempt only after an explicit ready action.

#### Scenario: Rejected task
- **WHEN** the reviewer rejects a task with feedback
- **THEN** the feedback SHALL be kept and no new attempt SHALL start before an explicit ready action
