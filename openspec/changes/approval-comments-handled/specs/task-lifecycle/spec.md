## ADDED Requirements

### Requirement: Approval comments need an outcome
An approval the owner decides with a reason other than the default SHALL record that reason as a board comment on the approval's task, linked to the approval, and the comment-outcome rules SHALL apply to it. The approval SHALL show the comment's outcome.

#### Scenario: Approved with a change
- **WHEN** the owner approves a screenshot sample with "the first screenshot without the callout"
- **THEN** the agent's next exit SHALL be refused until that comment has an outcome, and the approval SHALL show it
