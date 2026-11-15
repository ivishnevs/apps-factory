## ADDED Requirements

### Requirement: Telegram sends one message per cause
A cause that has a people topic item SHALL be sent to Telegram once, as that item; the raw attention line SHALL be sent only for attention rows that have no item.

#### Scenario: Escalation park
- **WHEN** a task is parked on an escalation to the owner and its topic item is added
- **THEN** Telegram SHALL send the item and SHALL NOT also send a raw line for the park
