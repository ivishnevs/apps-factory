## ADDED Requirements

### Requirement: Owner comments have an outcome
Every owner comment shown to a run SHALL get an outcome recorded by that run: done, forwarded with the recipient, or declined with a reason. Every exit tool SHALL refuse while a shown owner comment has no outcome. A run that ends without an exit SHALL leave the comment open, and a second such run SHALL raise an alert. Core SHALL copy every owner comment, with its outcome, into the project lead's conversation brief. A comment on a DONE or CANCELLED task SHALL be refused with the options to reopen the task with the comment or to ask the project lead.

#### Scenario: Out-of-role comment
- **WHEN** the owner comments "upload the fix to TestFlight" on a developer's task
- **THEN** the developer's run SHALL forward it to its manager or the project lead and record the outcome "forwarded"

#### Scenario: Exit without handling the comment
- **WHEN** a developer's run that was shown an owner comment calls `factory_exit_done` without an outcome for it
- **THEN** the tool SHALL refuse and list the unhandled comment, and the lead's brief SHALL carry the comment

#### Scenario: Comment on a done task
- **WHEN** the owner comments on a DONE task
- **THEN** Core SHALL refuse the comment and offer reopening the task with it or asking the lead
