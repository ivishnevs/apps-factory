## ADDED Requirements

### Requirement: Proposed decisions are actionable where people see them
A decision an agent proposes for the owner SHALL appear where people act (the Inbox, and the chat and Telegram
where it was raised) with the actions to approve or reject it, without the agent having to send a second request;
deciding it in one place SHALL clear it everywhere and tell the agent.

#### Scenario: Owner approves a proposal
- **WHEN** the producer proposes a decision that needs the owner
- **THEN** the owner SHALL see it in the Inbox with Approve and Reject, and approving it SHALL record it as
  approved and notify the producer

### Requirement: Choices are discussed and decided in the chat
A proposed decision with alternatives SHALL show all of them in the Inbox and the chat with the lead; the person SHALL
be able to pick one, propose another or discuss it in the chat, and the recorded decision SHALL be the person's final
choice. The Inbox item SHALL stay until it is decided and disappear when it is decided anywhere.

#### Scenario: Owner prefers an alternative after discussing it
- **WHEN** the producer proposes class names with a recommended option and two alternatives, and the owner discusses
  them in the chat and approves a changed second alternative there
- **THEN** that changed alternative SHALL be recorded as the approved decision and the Inbox item SHALL disappear

### Requirement: One decision, one place
A decision the person has already made by answering in the chat or an Inbox question SHALL be recorded from that
answer without a second approval request for the same thing, and the Inbox SHALL mirror pending items only, cleared
when they are answered or decided in the chat.

#### Scenario: Answer then approval
- **WHEN** the owner answers the producer's question "bo3 or bo5?" with an explicit choice of bo3
- **THEN** the decision bo3 SHALL be recorded with a reference to that answer and no approval asking to confirm bo3
  SHALL arrive in the Inbox

### Requirement: Inbox holds only what is open
Questions and decisions to approve SHALL be sent by the lead in the chat with the person; the Inbox SHALL list only
those still unanswered or undecided, each answerable there, and an item SHALL leave the Inbox as soon as it is
answered or decided in the chat or in the Inbox. The Inbox SHALL NOT keep answered or decided items as history.

#### Scenario: Decided in the chat
- **WHEN** the producer asks in the chat to approve a map rotation and the owner approves it in the chat
- **THEN** the matching Inbox item SHALL disappear and nothing about it SHALL remain in the Inbox
