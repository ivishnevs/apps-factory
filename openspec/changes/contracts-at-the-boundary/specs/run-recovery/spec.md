## ADDED Requirements

### Requirement: Setup failures name who fixes them
A run that fails before its agent starts SHALL report a failure code and a fixer: `implementer` when the handed-over work is missing or unusable, `node` for node faults, `owner` for what only the owner provides (local policy, credentials, secrets), or `none` for transient causes.

#### Scenario: Not reviewable
- **WHEN** a review copy cannot be made because the handed-over work is not on the remote
- **THEN** the run SHALL end `setup_failed` with the fixer `implementer`

### Requirement: Failures route by their fixer
Core SHALL route a setup failure by its fixer: `implementer` returns the task to its implementer as rework with the cause in the brief, without counting the failed run's attempt (an implementer no longer on the team routes to its manager); `node` follows node readiness; `owner` opens one owner item without retrying first; `none` follows the bounded automatic retry.

#### Scenario: Review copy cannot be made
- **WHEN** a review run ends `setup_failed` because the handed-over work cannot be checked out
- **THEN** the task SHALL return to its implementer with the cause, the reviewer's attempt SHALL NOT be counted, and no escalation SHALL be opened

#### Scenario: Transient failure
- **WHEN** a run fails at setup on lock contention
- **THEN** Core SHALL retry it under the bounded automatic retry

### Requirement: A deterministic failure is not retried as is
When a task's run fails with the same failure code on the same handed-over head twice in a row, Core SHALL treat the failure as deterministic and route it by its fixer instead of retrying the same agent again; an unclassified repeated failure SHALL route to the implementer when the task has handed-over work and to the escalation otherwise.

#### Scenario: Same failure twice
- **WHEN** a reviewer's run fails twice with the same code while the handed-over head is unchanged
- **THEN** Core SHALL NOT start a third identical run and SHALL route the task by the failure's fixer

### Requirement: Every escalation offers an answer that can clear it
Each escalation reason SHALL offer at least one answer other than retry and cancel that changes who acts or what they act on. `retries_exhausted` SHALL offer `reassign`, and on a task in review also `return`, which sends the task back to its implementer with the cause.

#### Scenario: Escalated review
- **WHEN** a task in review is escalated `retries_exhausted`
- **THEN** its answers SHALL include `return`, and answering `return` SHALL give the task to its implementer with the cause in the brief
