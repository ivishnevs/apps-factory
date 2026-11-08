## ADDED Requirements

### Requirement: Project milestones
Core SHALL keep milestones per project with a title, a goal, a "done when", an order, a status (`planned`, `active`,
`done`, `deferred`) and the OpenSpec changes and tasks that belong to it. Leads SHALL create and edit milestones through
Factory tools. Making a milestone active, deferring it or reordering milestones SHALL be an owner decision. By default
at most one milestone per project SHALL be active.

#### Scenario: Owner activates the next milestone
- **WHEN** the producer asks to make M5 active while M3 is active
- **THEN** M5 SHALL become active only after the owner approves, and the request SHALL say what happens to M3

### Requirement: Work outside the active milestone is marked
A task a lead creates that belongs to no active milestone SHALL be created in BACKLOG or carry an `off_milestone`
marker with a reason, and the board SHALL show the marker.

#### Scenario: QA follow-up outside the milestone
- **WHEN** the producer creates a bug-fix task for a finished milestone while M5 is active
- **THEN** the task SHALL show `off_milestone` with the producer's reason

### Requirement: Milestone progress for the owner
The home page and the board SHALL show each project's active milestone with its goal, done, open and blocked task
counts, what blocks it, tasks created and closed per day, the next milestone, and an ETA labelled as an estimate.

#### Scenario: Slicing a change does not read as growth
- **WHEN** the producer slices a new change of the active milestone into eight tasks
- **THEN** the panel SHALL show them as created for that milestone next to the tasks closed that day

### Requirement: Milestone report
When every task of an active milestone is done, the lead SHALL send the owner a milestone report (what shipped, the
build or commit to play, what to check, known gaps) as an inbox item, and the milestone SHALL become `done` only when
the owner accepts it.

#### Scenario: Owner sends a milestone back
- **WHEN** the owner sends the M5 report back with notes
- **THEN** M5 SHALL stay active and the lead SHALL receive the notes as work for M5
