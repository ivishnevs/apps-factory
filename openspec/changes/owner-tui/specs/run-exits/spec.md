## ADDED Requirements

### Requirement: Task exits for the owner
Core SHALL list a task's accepted exits at `GET /tasks/{id}/exits`, oldest first, each with its run, kind, time and the node's git and forge facts (head, branch, ahead of default, pushed, pull request number, state, head and URL), and SHALL answer 404 for an unknown task.

#### Scenario: Submitted and approved task
- **WHEN** the owner lists the exits of a task that was submitted and then approved by its reviewer
- **THEN** Core SHALL return the submit and the handoff in that order, the handoff with its pull request facts

#### Scenario: Unknown task
- **WHEN** the owner lists the exits of a task that does not exist
- **THEN** Core SHALL answer 404
