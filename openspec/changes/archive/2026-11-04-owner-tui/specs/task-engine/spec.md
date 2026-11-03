## ADDED Requirements

### Requirement: Task parks for the owner
Core SHALL list a task's parks at `GET /tasks/{id}/parks`, oldest first, each with its id, kind, ref, reason, who clears it (`owner`, or `agent:<name>` for a question to an agent), status, run and the times it was set and cleared: the active parks, and with `?all=1` also the cleared and overridden ones. An unknown task SHALL be 404.

#### Scenario: Blocked task
- **WHEN** the owner lists the parks of a task blocked by hand
- **THEN** Core SHALL return its active `block` park with the reason, cleared by the owner

#### Scenario: Cleared park
- **WHEN** the task was readied again
- **THEN** the default list SHALL be empty and `?all=1` SHALL return the park as cleared with its time
