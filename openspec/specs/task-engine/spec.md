# task-engine Specification

## Purpose
Define tasks, their scheduling, their review and how a task parks for the owner.

## Requirements

### Requirement: Tasks
Core SHALL validate lifecycle actions, same-project DAG dependencies and optimistic versions.
Dependencies SHALL only unblock scheduling after DONE, not runtime completion or submission.
Duplicate mutation IDs SHALL replay the original response without repeated effects.

#### Scenario: Dependency submitted
- **WHEN** a task's dependency is submitted to IN_REVIEW
- **THEN** the task SHALL stay unschedulable until the dependency is DONE

### Requirement: Scheduling
Dispatch SHALL atomically persist task IN_PROGRESS and a unique attempt/run in the queued state.
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

### Requirement: Task parks for the owner
Core SHALL list a task's parks at `GET /tasks/{id}/parks`, oldest first, each with its id, kind, ref, reason, who clears it (`owner`, or `agent:<name>` for a question to an agent), status, run and the times it was set and cleared: the active parks, and with `?all=1` also the cleared and overridden ones. An unknown task SHALL be 404.

#### Scenario: Blocked task
- **WHEN** the owner lists the parks of a task blocked by hand
- **THEN** Core SHALL return its active `block` park with the reason, cleared by the owner

#### Scenario: Cleared park
- **WHEN** the task was readied again
- **THEN** the default list SHALL be empty and `?all=1` SHALL return the park as cleared with its time
