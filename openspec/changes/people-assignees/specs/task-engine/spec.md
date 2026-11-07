## ADDED Requirements

### Requirement: Tasks assigned to people
A task's assignee MAY be a person of the company (`member:<user>`). Core SHALL NOT schedule or dispatch runs for it
and SHALL show its wait reason as `with_person`. The person and the owner MAY take a task without a live run, start
it, mark it done (with an optional pull request) and give it back to an agent. A done action with a pull request on a
connected repository SHALL move the task to IN_REVIEW and to DONE when the pull request is merged; without one the
task SHALL be DONE at once. Tasks that depend on a person's task SHALL wait in the normal dependency wait and SHALL
become schedulable when it is DONE.

#### Scenario: Owner takes a task on the board
- **WHEN** the owner presses "Take it" on a READY task with no live run
- **THEN** the task SHALL be assigned to the owner and no run SHALL be dispatched for it

#### Scenario: Dependents continue after the merge
- **WHEN** tasks A and B depend on the owner's task P and the owner marks P done with pull request #200
- **THEN** P SHALL be IN_REVIEW until #200 is merged, then DONE, and A and B SHALL be scheduled without an
  escalation or manual action

#### Scenario: Give back
- **WHEN** the person gives the task back to an agent
- **THEN** the task SHALL be assigned to that agent and scheduled as usual

### Requirement: A person's task is not a stall
A dependency on a person's task SHALL NOT open a `stalled` escalation. When a person's task has not changed for the
company's `people.remind_after` (default 72h) while other tasks wait on it, Core SHALL send that person one reminder
per episode naming the waiting tasks.

#### Scenario: Long wait on a person
- **WHEN** an agent's task has waited 4 days on the owner's unchanged task
- **THEN** there SHALL be no `stalled` escalation and the owner SHALL have received one reminder
