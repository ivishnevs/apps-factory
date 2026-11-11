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

### Requirement: The lead sees every board comment
Core SHALL put every owner comment on a task of a project, with the assignee's outcome, into the project lead's conversation brief without waking the lead.

#### Scenario: Board's word on another agent's task
- **WHEN** the owner comments "upload to TestFlight" on developer-lite's task in tracely
- **THEN** the CEO's next tracely brief SHALL carry that comment and its outcome, and the CEO SHALL NOT be woken for it

### Requirement: The lead is woken only when needed
Core SHALL wake the project lead for an owner comment when its outcome is declined, when no outcome is "done" while the task stays active past the team's limit, or at once when the comment is in a locked category. A forward to a manager who is the lead SHALL be one message, and the lead's outcome SHALL be a linked task or a refusal with a reason.

#### Scenario: Declined comment
- **WHEN** the assignee declines an owner comment
- **THEN** the lead SHALL be woken to confirm or override it

### Requirement: No comments on closed tasks
Core SHALL refuse an owner comment on a DONE or CANCELLED task and SHALL name the options to reopen the task with the comment or to ask the project lead.

#### Scenario: Comment on a done task
- **WHEN** the owner comments on a DONE task
- **THEN** Core SHALL refuse the comment and offer reopening the task with it or asking the lead
