## ADDED Requirements

### Requirement: Conversation messages can be answered from the Inbox
An Inbox item that belongs to a conversation SHALL offer a reply to that conversation and a link to it, not only
"Mark as read".

#### Scenario: Lead replies with questions
- **WHEN** the producer's reply in the chat asks the owner to choose between options
- **THEN** the owner SHALL be able to answer it from the Inbox item, and the link SHALL open that conversation

#### Scenario: Answered in the chat
- **WHEN** the owner answers the producer's question in the chat instead of the Inbox
- **THEN** the Inbox item SHALL disappear
