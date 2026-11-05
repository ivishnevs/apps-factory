## ADDED Requirements

### Requirement: Telegram for every company
Every company SHALL be able to use its own Telegram bot, and each of its people SHALL be able to bind their own chat
from the web and receive there what is meant for them.

#### Scenario: Binding a chat
- **WHEN** a member opens the connect link from Settings and starts the company's bot
- **THEN** that chat receives the company's notifications for that member

### Requirement: Clear addressee in a chat with several managers
A message from an agent SHALL name its sender and team; a reply SHALL reach exactly that message's sender, `@name`
SHALL reach that agent, and plain text SHALL be a reply to the last message delivered to the person; the bot SHALL
confirm where each message went.

#### Scenario: Two teams' managers
- **WHEN** the lead of team A wrote, then the lead of team B wrote, and the person sends plain text
- **THEN** it goes to the lead of team B as a reply to that message and the bot confirms where it went
