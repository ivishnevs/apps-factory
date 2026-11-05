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
