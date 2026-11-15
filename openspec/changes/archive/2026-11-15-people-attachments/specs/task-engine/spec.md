## ADDED Requirements

### Requirement: Comments carry images
A task comment, a reopen comment and an ask-lead request SHALL accept the same image attachments as a person's
message, with the same limits. The task view SHALL show them inline in the comment, and the run that handles the
comment SHALL get them as described for messages.

#### Scenario: Bug screenshot on a task
- **WHEN** the owner comments on a task "the button is cut off on iPhone SE" with a screenshot
- **THEN** the comment SHALL show the screenshot, and the assignee's run that handles the comment SHALL have the file in its worktree with its path in the brief
