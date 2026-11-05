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
