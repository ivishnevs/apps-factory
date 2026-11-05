## ADDED Requirements

### Requirement: Replies return to where the person asked
An agent's reply to a message a person sent from Telegram SHALL be delivered to that person's Telegram chat as
well as the web conversation, and SHALL NOT be sent to other people's Telegram chats.

#### Scenario: Two people, one lead
- **WHEN** the owner and an admin each write to the lead from their own Telegram chats
- **THEN** each SHALL get the lead's answer to their own message in their own Telegram chat, and both answers
  SHALL be in the web conversation with their senders shown
