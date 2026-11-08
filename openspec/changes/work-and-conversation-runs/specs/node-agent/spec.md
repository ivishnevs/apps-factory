## ADDED Requirements

### Requirement: Conversation slots
A node SHALL offer `conversation_slots` (default 1) in addition to `slots`. A conversation run SHALL take a free
conversation slot first and a work slot only when no conversation slot is free; a work run SHALL never take a
conversation slot.

#### Scenario: Full node still answers
- **WHEN** a node with 4 slots and 1 conversation slot runs 4 work runs and a conversation run is queued for it
- **THEN** the node SHALL claim the conversation run in its conversation slot

### Requirement: Short session wait for conversations
A queued conversation run SHALL wait for the node holding its session for at most the company's
`session_affinity.conversation_wait` (default 5 minutes), then SHALL be claimable by any node with a free slot as a
fresh session whose brief carries the recent conversation history.

#### Scenario: Session on a paused node
- **WHEN** the producer's conversation session is on a paused node and the owner writes in the chat
- **THEN** after at most 5 minutes another node SHALL start a fresh producer conversation run with the recent history
