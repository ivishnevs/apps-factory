## ADDED Requirements

### Requirement: Owner comments have an outcome
Every owner comment shown to a run SHALL get an outcome recorded by that run: done, forwarded with the recipient, or declined with a reason. Every exit tool SHALL refuse while a shown owner comment has no outcome.

#### Scenario: Out-of-role comment
- **WHEN** the owner comments "upload the fix to TestFlight" on a developer's task
- **THEN** the developer's run SHALL forward it to its manager or the project lead and record the outcome "forwarded"

#### Scenario: Exit without handling the comment
- **WHEN** a developer's run that was shown an owner comment calls `factory_exit_done` without an outcome for it
- **THEN** the tool SHALL refuse and list the unhandled comment

### Requirement: Unhandled comments stay open
A run that ends without an exit SHALL leave its owner comments open for the next run, and a second such run SHALL raise an alert to the owner.

#### Scenario: Run crashes after seeing a comment
- **WHEN** two runs in a row end without an exit after being shown the same owner comment
- **THEN** the comment SHALL still be open and the owner SHALL get an alert

### Requirement: The lead gets every board comment
Core SHALL copy every owner comment on a task of a project, with the assignee's outcome, into the project lead's conversation brief, and SHALL ask the lead to act when the outcome is forwarded or declined.

#### Scenario: Board's word on another agent's task
- **WHEN** the owner comments "upload to TestFlight" on developer-lite's task in tracely
- **THEN** the CEO's tracely brief SHALL carry that comment and its outcome

### Requirement: No comments on closed tasks
Core SHALL refuse an owner comment on a DONE or CANCELLED task and SHALL name the options to reopen the task with the comment or to ask the project lead.

#### Scenario: Comment on a done task
- **WHEN** the owner comments on a DONE task
- **THEN** Core SHALL refuse the comment and offer reopening the task with it or asking the lead
