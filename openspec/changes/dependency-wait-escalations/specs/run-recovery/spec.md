## MODIFIED Requirements

### Requirement: Stall detection
Core SHALL open a self-clearing `stalled` escalation when a task waits on a task that is unassigned or cancelled, and
when it waits on a task in BACKLOG that has not changed for the company's `stalls.backlog_after` (default 24h). The
escalation SHALL go to the assignee's manager (the owner only at the top) and SHALL offer `keep_waiting`,
`drop_dependency` and `cancel`; `unblock` SHALL be accepted as `drop_dependency`. Core SHALL emit one alert per
episode for tasks idle or without progress longer than the configured intervals.

#### Scenario: Blocked on an unassigned task
- **WHEN** task A is blocked on task B and B has no assignee
- **THEN** A SHALL get a `stalled` escalation naming B, which SHALL clear when B is assigned

#### Scenario: Planned BACKLOG dependency
- **WHEN** task A is READY with a dependency on task B, B is in BACKLOG and B changed 2 hours ago
- **THEN** A SHALL have no `stalled` escalation and SHALL stay in its dependency wait

#### Scenario: BACKLOG dependency without movement
- **WHEN** B has stayed in BACKLOG without any change for longer than `stalls.backlog_after`
- **THEN** A SHALL get a `stalled` escalation to its assignee's manager naming B, which SHALL clear when B leaves
  BACKLOG

#### Scenario: Keep waiting
- **WHEN** the manager answers the escalation with `keep_waiting`
- **THEN** the escalation SHALL close, A SHALL keep its dependency on B, and no new `stalled` escalation for A and B
  SHALL open until B changes or another `stalls.backlog_after` passes

#### Scenario: Drop dependency
- **WHEN** the manager answers with `drop_dependency` (or the legacy `unblock`)
- **THEN** B SHALL be removed from A's dependencies, with a comment naming B

#### Scenario: Parked on a child in BACKLOG
- **WHEN** task A is parked on its children and an open child C is in BACKLOG past `stalls.backlog_after` or has no
  assignee
- **THEN** A SHALL get a `stalled` escalation to its assignee's manager naming C, which SHALL clear when C is assigned
  and released or closes, and the answer `drop_dependency` SHALL release A from its children park
