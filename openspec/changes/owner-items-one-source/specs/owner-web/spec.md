## ADDED Requirements

### Requirement: Surfaces render Core's person items
The Inbox, the task page, Home, the board, project counts, the nav badge, task headlines and review actions SHALL show what a person is asked to do only from Core's person items, and SHALL NOT derive such items from messages, task verdicts or clearers. The Inbox reviews lane SHALL list Core's review items only.

#### Scenario: Escalated task in review
- **WHEN** a task in review with an agent reviewer has an open escalation to the owner
- **THEN** the Inbox SHALL show it once, as the escalation, and SHALL NOT list it under reviews

### Requirement: The task page shows the Inbox card
The task page SHALL render the task's open items with the same card, text and actions as the Inbox, with a link to the item's topic in the Inbox, and an answer given on either SHALL be the same answer.

#### Scenario: Answer from the task page
- **WHEN** the owner answers an item on the task page
- **THEN** the item SHALL be settled in the Inbox too, and the task page SHALL have linked to that topic
