## ADDED Requirements

### Requirement: Every agent has a reviewer
The catalog SHALL resolve a reviewer for every agent: an agent named by a review rule, or people stated
explicitly. A configuration with an agent whose work would go to review with no reviewer SHALL NOT be saved
silently.

#### Scenario: Role without a review rule
- **WHEN** an owner saves a team where the game designer is covered by no review rule and no explicit people
  reviewer
- **THEN** Core SHALL refuse the save (or save it with a warning) naming the agent without a reviewer

#### Scenario: Submitted task shows its reviewer
- **WHEN** an agent whose reviewer is people submits a task
- **THEN** the task SHALL show its reviewer as "Owner" (or the person), never empty, on the task and the board

### Requirement: Reviewer required outside the backlog
Every task outside BACKLOG SHALL have an explicit reviewer (an agent or a person). Making a task ready, creating it as
ready, or any later transition SHALL be refused without one, for agents (tool error) and people (web and TUI error
"Reviewer is required").

#### Scenario: Agent makes a task ready without a reviewer
- **WHEN** the producer calls task ready on a task with no reviewer
- **THEN** Core SHALL refuse it naming the missing reviewer and the policy's suggestion, and the task SHALL stay in
  the backlog

#### Scenario: Owner in the web
- **WHEN** the owner makes a backlog task ready without choosing a reviewer
- **THEN** the web SHALL show "Reviewer is required" and the task SHALL not change
