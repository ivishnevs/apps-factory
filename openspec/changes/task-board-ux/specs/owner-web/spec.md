## ADDED Requirements

### Requirement: Task opens over the board
The web SHALL open a task from the board over the board (a panel on desktop, a full-screen sheet on phone) with a
shareable `/tasks/{id}` URL, and closing it SHALL return to the board exactly as it was (filters, open columns,
scroll). Reloading or opening the URL directly SHALL show the task's full page.

#### Scenario: Looking through tasks
- **WHEN** the owner opens a card in the Waiting column, reads it and presses Esc
- **THEN** the board SHALL show the same columns, filters and scroll position as before, with no reload

#### Scenario: Next task
- **WHEN** the owner presses next in an open task
- **THEN** the panel SHALL show the next task of the same column

### Requirement: One status vocabulary
The board's column for a task SHALL be the status shown for it everywhere in the web (card, task panel, task page,
Home).

#### Scenario: Task waiting for dependencies
- **WHEN** a READY task waits for its dependencies
- **THEN** its card, panel and page SHALL all call it Waiting and name the task it waits on

### Requirement: Capacity reasons name the limit
A queued task SHALL say which limit holds it: the agent's concurrent-run limit (with the agent and its running
tasks), the project's workspace, or full nodes; "All nodes busy" SHALL be shown only when the capable nodes are full.

#### Scenario: Agent at its limit
- **WHEN** gameplay-dev runs 2 tasks with `max_concurrent_runs: 2` and a third is ready while the node has free slots
- **THEN** the third SHALL show "gameplay-dev runs 2 of 2" with links to the running tasks, not "All nodes busy"
