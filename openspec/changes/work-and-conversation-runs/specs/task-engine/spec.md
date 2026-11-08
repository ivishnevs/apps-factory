## ADDED Requirements

### Requirement: Work and conversation run limits
Core SHALL count an agent's live runs separately for work runs and conversation runs. The agent's
`max_concurrent_runs` limits work runs only; `execution.conversation.max_concurrent_runs` limits conversation runs.
A conversation run SHALL NOT wait for the agent's work runs unless the owner configured a shared limit.

#### Scenario: Question while the agent works
- **WHEN** network-dev has work limit 1, one live work run, and the producer sends it a question
- **THEN** its conversation task SHALL be claimable at once and SHALL NOT report `capacity` for the work run

#### Scenario: Work limit unchanged
- **WHEN** network-dev has work limit 2, two live work runs and a live conversation run
- **THEN** a third work task SHALL wait for the work limit

### Requirement: Why a conversation waits
While a conversation task has unhandled messages and no live run for them, its clearer SHALL name the cause in plain
words: the live conversation run and the senders it handles, the conversation limit and where it is used, full or
paused nodes, or a shared work limit. The chat SHALL show that text under the unhandled message.

#### Scenario: Busy answering others
- **WHEN** a message from the owner arrives while gameplay-dev's conversation run handles messages from the producer
  and network-dev
- **THEN** the chat SHALL show that gameplay-dev is answering the producer and network-dev and the owner's message is
  next
