## ADDED Requirements

### Requirement: Sessions do not cross accounts
A node SHALL record the runtime account each session ran under and SHALL NOT resume a session under a different
account. Such a run SHALL start a fresh session whose prompt says the context comes from Core, as on a new node.
When the owner continues on a changed account, the node SHALL treat all of that runtime's sessions as belonging to
the previous account.

#### Scenario: Account changed and accepted
- **WHEN** a node's Claude login changes, the owner continues on the new account, and the next run of a task that
  had a session on that node starts
- **THEN** the run SHALL start a fresh session with the Core-context note and SHALL NOT resume the old session

### Requirement: A lost session asks the owner
When a node recorded a session for a task and agent, the account is unchanged, and the runtime no longer has that
session, the node SHALL NOT start the run and SHALL report `session_lost`. Core SHALL open one escalation per task
and agent asking the owner to continue with a new session or wait. A task that moved to another node SHALL NOT
cause this escalation.

#### Scenario: Transcript deleted
- **WHEN** the transcript of a task's session is deleted on the node that ran it and the task's next run is placed
  there
- **THEN** the owner SHALL get an Inbox item with Continue and Wait, and no run SHALL start until they answer

#### Scenario: Owner continues
- **WHEN** the owner answers Continue
- **THEN** the run SHALL be queued again and start a fresh session with the Core-context note
