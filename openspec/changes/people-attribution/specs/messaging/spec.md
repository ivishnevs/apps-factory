## ADDED Requirements

### Requirement: People are attributed
Every action and message a person makes through the web or Telegram SHALL record that person, and the UI and the
agents' briefs SHALL show who it was, not only "owner".

#### Scenario: Admin creates a task
- **WHEN** an admin of the company creates a task in the web
- **THEN** the task and the activity feed SHALL show that admin as its creator

#### Scenario: Two people write to the lead
- **WHEN** the owner and an admin both write in the chat with the team lead
- **THEN** each message SHALL show its sender, and the lead's brief SHALL name who wrote each message
