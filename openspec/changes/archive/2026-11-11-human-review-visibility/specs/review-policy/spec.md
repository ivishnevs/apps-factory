## ADDED Requirements

### Requirement: Human review is visible
A task that reaches review without an agent reviewer SHALL name the person it waits for and SHALL notify that
person in the Inbox and Telegram with the actions to accept it or send it back; people assigning such work SHALL
be told beforehand that it will come back to them.

#### Scenario: Lead submits a task
- **WHEN** the team lead submits a task a person created and no review rule covers the lead
- **THEN** the task SHALL show "waiting for review by <person>" and that person SHALL get an Inbox item and a
  Telegram message with Accept and Send back
