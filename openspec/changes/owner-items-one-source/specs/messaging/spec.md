## ADDED Requirements

### Requirement: Core computes what is asked of a person
Every request for a person to act — a question, an approval, a decision, an escalation, and a review where the person is the reviewer — SHALL be an item in that person's people topics, and Core SHALL expose each task's open items with their topic, kind, person and actions. A task SHALL be shown as needing a person exactly when it has an open item waiting for that person.

#### Scenario: Escalated review by an agent
- **WHEN** a task in review with an agent reviewer is escalated to the owner
- **THEN** the task SHALL have exactly one open owner item, the escalation, and no review item for the owner

#### Scenario: Owner is the reviewer
- **WHEN** a task enters review with the owner as its reviewer
- **THEN** Core SHALL create a review item in the owner's topics and settle it when the review ends

### Requirement: Escalation briefs name the task and the cause
An escalation's people brief SHALL be titled with the task's title and the cause in people words, SHALL NOT carry the raw escalation body, and every escalation reason and answer SHALL have its people texts, checked by a test.

#### Scenario: Review cannot start
- **WHEN** a task "Audit and onboard Unsealed" is escalated because its review cannot start
- **THEN** the item's title SHALL name "Audit and onboard Unsealed" and the cause, not only the agent and a generic group
