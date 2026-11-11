## ADDED Requirements

### Requirement: Comment outcomes and closed-task actions on the task page
The task page SHALL show each owner comment's outcome (done, forwarded to whom, declined and why) or "not handled yet", and SHALL offer no comment box on a DONE or CANCELLED task, offering "Reopen with comment" and "Ask the lead about it" instead.

#### Scenario: Done task
- **WHEN** the owner opens a DONE task
- **THEN** the page SHALL show "Reopen with comment" and "Ask the lead about it" and no comment box
