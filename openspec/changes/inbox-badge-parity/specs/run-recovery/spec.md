## ADDED Requirements

### Requirement: No progress alert skips recorded waits
The `no_progress` alert SHALL NOT be raised for a task whose state records whom it waits for (a parked wait, a wait on
the owner or another agent, a pinned or queued wait); it SHALL still be raised for runs that make no progress without
a recorded wait.

#### Scenario: Producer waits for the owner
- **WHEN** a parked producer task runs three times and each run ends "still waiting for the owner's answer"
- **THEN** no `no_progress` alert SHALL be raised and the task SHALL show that it waits for the owner
