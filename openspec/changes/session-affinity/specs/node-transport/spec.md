## ADDED Requirements

### Requirement: Session affinity
A queued run that continues a session SHALL be offered to and claimable by only the node that holds the session,
while that node is online, not paused, able to run the agent, and the run has waited less than the affinity wait.
Otherwise the affinity SHALL be released: any capable node MAY take the run, start a fresh session, and become the
session's node. A run without a session to continue SHALL have no affinity. The pin of unpushed work SHALL keep
its own rules.

#### Scenario: Conversation continues on its node
- **WHEN** two nodes are idle and a message continues a conversation whose session is on the first node
- **THEN** only the first node SHALL get and claim the run, and it SHALL resume the session

#### Scenario: Holder offline
- **WHEN** the node holding a task's session is offline beyond the node offline timeout and the task's work is pushed
- **THEN** another capable node SHALL take the run and start a fresh session with the context from Core

#### Scenario: Holder busy too long
- **WHEN** the holder has no free slot for longer than the affinity wait
- **THEN** the affinity SHALL be released and the task SHALL say so
