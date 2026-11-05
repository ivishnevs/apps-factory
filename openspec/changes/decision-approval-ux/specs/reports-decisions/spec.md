## ADDED Requirements

### Requirement: Proposed decisions are actionable where people see them
A decision an agent proposes for the owner SHALL appear where people act (the Inbox, and the chat and Telegram
where it was raised) with the actions to approve or reject it, without the agent having to send a second request;
deciding it in one place SHALL clear it everywhere and tell the agent.

#### Scenario: Owner approves a proposal
- **WHEN** the producer proposes a decision that needs the owner
- **THEN** the owner SHALL see it in the Inbox with Approve and Reject, and approving it SHALL record it as
  approved and notify the producer
