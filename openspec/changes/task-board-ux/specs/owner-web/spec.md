## ADDED Requirements

### Requirement: One status vocabulary
The board's column for a task SHALL be the status shown for it everywhere in the web (card, task page,
Home, TUI, Telegram).

The columns SHALL be Backlog, Blocked, To do, In progress, In review and Done.

#### Scenario: Task waiting for dependencies
- **WHEN** a READY task waits for its dependencies
- **THEN** its card and page SHALL call it Blocked and name the task it waits on, and the task header SHALL NOT
  show "ready"

#### Scenario: Runnable task
- **WHEN** a READY task can start
- **THEN** it SHALL be shown as To do

### Requirement: Needs action apart from Blocked
The board SHALL show tasks that cannot move without someone acting (a person's decision, approval, answer or review;
an escalation; a run to inspect; status BLOCKED; no capable or online connector; no assignee; a pause) apart from
Blocked (the column name is open: Needs action proposed), each with who has to act and what to do, and SHALL keep in
Blocked only tasks that start on their own in their turn (dependencies, artifacts in progress, timer waits, questions
to agents).

#### Scenario: Escalated task
- **WHEN** a task is parked by an escalation to the owner
- **THEN** it SHALL be shown as needing action, first, marked as waiting on you, with what to do

#### Scenario: Dependency wait
- **WHEN** a task waits only for another task to finish
- **THEN** it SHALL be in Blocked, not among the tasks needing action

### Requirement: Capacity reasons name the limit
A queued task SHALL say which limit holds it: the agent's concurrent-run limit (with the agent and its running
tasks), the project's workspace, or full nodes; "All nodes busy" SHALL be shown only when the capable nodes are full.

#### Scenario: Agent at its limit
- **WHEN** gameplay-dev runs 2 tasks with `max_concurrent_runs: 2` and a third is ready while the node has free slots
- **THEN** the third SHALL show "gameplay-dev runs 2 of 2" with links to the running tasks, not "All nodes busy"
