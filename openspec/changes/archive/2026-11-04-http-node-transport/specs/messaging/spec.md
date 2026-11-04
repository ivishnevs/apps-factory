## MODIFIED Requirements

### Requirement: Durable messages and conversations
Core SHALL persist messages and conversations in the transaction of their cause, with mutation replay.

#### Scenario: Repeated send
- **WHEN** an agent repeats a message send with the same request key
- **THEN** Core SHALL replay the original response without a second message
