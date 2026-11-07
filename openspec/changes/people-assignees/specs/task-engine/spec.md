## ADDED Requirements

### Requirement: Tasks assigned to people
A task's assignee MAY be a person of the company (`member:<user>`), set in the same assignee field as an agent. Such
a task SHALL use the same statuses, transitions, review and merge rules as any task; Core SHALL NOT dispatch runs
for it, and the assigned person and the owner SHALL be able to make the transitions an agent's run makes. Tasks that
depend on a person's task SHALL wait in the normal dependency wait and SHALL become schedulable when it is DONE.

#### Scenario: Owner takes a task on the board
- **WHEN** the owner picks themself as the assignee of a READY task with no live run
- **THEN** the task SHALL stay READY, assigned to the owner, and no run SHALL be dispatched for it

#### Scenario: Dependents continue after the merge
- **WHEN** tasks A and B depend on the owner's task P and the owner moves P to IN_REVIEW with pull request #200
- **THEN** P SHALL become DONE when its reviewer approves and #200 is merged, and A and B SHALL be scheduled without an
  escalation or manual action

#### Scenario: Give back
- **WHEN** the person reassigns the task to an agent
- **THEN** the task SHALL be scheduled for that agent as usual

### Requirement: A person's task is not a stall
A dependency on a person's task SHALL NOT open a `stalled` escalation. When a person's task has not changed for the
company's `people.remind_after` (default 72h) while other tasks wait on it, Core SHALL send that person one reminder
per episode naming the waiting tasks.

#### Scenario: Long wait on a person
- **WHEN** an agent's task has waited 4 days on the owner's unchanged task
- **THEN** there SHALL be no `stalled` escalation and the owner SHALL have received one reminder
