## ADDED Requirements

### Requirement: Owner comments have an outcome
Every owner comment shown to a run SHALL get an outcome recorded by that run: done, forwarded with the recipient, or declined with a reason. A comment without an outcome SHALL stay an open trigger, and a second run that skips it SHALL raise an alert. A comment on a DONE or CANCELLED task SHALL be refused with the options to reopen the task with the comment or to ask the project lead.

#### Scenario: Out-of-role comment
- **WHEN** the owner comments "upload the fix to TestFlight" on a developer's task
- **THEN** the developer's run SHALL forward it to its manager or the project lead and record the outcome "forwarded"

#### Scenario: Comment on a done task
- **WHEN** the owner comments on a DONE task
- **THEN** Core SHALL refuse the comment and offer reopening the task with it or asking the lead
