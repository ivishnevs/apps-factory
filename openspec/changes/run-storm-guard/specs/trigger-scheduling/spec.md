## ADDED Requirements

### Requirement: No run the claim would refuse
Dispatch SHALL evaluate the task as the claim re-check does, with the run being queued as its live run, and SHALL NOT queue a run the claim would end as `stale`. A task whose runs ended `stale` three times within ten minutes SHALL get no new run until that window passes, and the event feed SHALL record one `task.dispatch_throttled` event per window.

#### Scenario: Parked task
- **WHEN** any dispatcher dispatches a READY task parked on an escalation
- **THEN** Dispatch SHALL answer conflict and no run SHALL be queued

#### Scenario: Repeated stale runs
- **WHEN** a task had three runs end `stale` within ten minutes and is dispatched again
- **THEN** Dispatch SHALL answer conflict, the feed SHALL show one `task.dispatch_throttled` event for the window, and the task SHALL be dispatched normally after the window
