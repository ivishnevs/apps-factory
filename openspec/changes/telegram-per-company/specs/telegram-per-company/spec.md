## ADDED Requirements

### Requirement: Telegram for every company
Every company SHALL be able to use its own Telegram bot, and each of its people SHALL be able to bind their own chat
from the web and receive there what is meant for them.

#### Scenario: Binding a chat
- **WHEN** a member opens the connect link from Settings and starts the company's bot
- **THEN** that chat receives the company's notifications for that member

### Requirement: Clear addressee in a chat with several managers
A message from an agent SHALL name its sender and team; a reply SHALL reach exactly that sender's conversation; free
text SHALL go to the addressee the bot shows, and the bot SHALL ask when the addressee is unclear.

#### Scenario: Two teams' managers
- **WHEN** the leads of two teams wrote to a person and the person sends plain text without a reply or addressee
- **THEN** the bot asks which lead it is for and sends it only after the person chooses
