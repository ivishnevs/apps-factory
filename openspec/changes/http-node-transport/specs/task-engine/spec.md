## MODIFIED Requirements

### Requirement: Scheduling
Dispatch SHALL atomically persist task IN_PROGRESS and a unique attempt/run in the queued state.
Scheduler SHALL use priority and availability without fixed role sequences. Restart SHALL recover
from persisted state. Unknown outcomes SHALL quarantine the workspace and block automatic retry.

#### Scenario: Unknown outcome
- **WHEN** a run's outcome is unknown
- **THEN** the workspace SHALL be quarantined and the task SHALL NOT be retried automatically
