## ADDED Requirements

### Requirement: Core computes what is asked of a person
Every request for a person to act — a question, an approval, a decision, an escalation, and a review where the person is the reviewer — SHALL be an item in that person's people topics, and Core SHALL expose each task's open items with their topic, kind, person and actions. A task SHALL be shown as needing a person exactly when it has an open item waiting for that person.

#### Scenario: Escalated review by an agent
- **WHEN** a task in review with an agent reviewer is escalated to the owner
- **THEN** the task SHALL have exactly one open owner item, the escalation, and no review item for the owner

#### Scenario: Owner is the reviewer
- **WHEN** a task enters review with the owner as its reviewer
- **THEN** Core SHALL create a review item in the owner's topics and settle it when the review ends

#### Scenario: A member is the reviewer
- **WHEN** a task enters review with a member as its reviewer
- **THEN** its review item SHALL wait for that member, not for the owner

#### Scenario: A parked review
- **WHEN** a task in a person's review is held by an active park
- **THEN** its review item SHALL NOT wait while the park holds it; the park's own item is what the person sees

#### Scenario: Tasks already in review
- **WHEN** a task is in a person's review without a review item, or a review item's task left that review
- **THEN** the scheduler's sweep SHALL add the item, or settle it

#### Scenario: No alert beside an item
- **WHEN** a task has an open item waiting for a person
- **THEN** the alerts SHALL NOT list that task

### Requirement: Escalation briefs name the task and the cause
An escalation's people brief SHALL be titled with the task's title and the cause in people words, SHALL NOT carry the raw escalation body, and every escalation reason and answer SHALL have its people texts, checked by a test.

#### Scenario: Review cannot start
- **WHEN** a task "Audit and onboard Unsealed" is escalated because its review cannot start
- **THEN** the item's title SHALL name "Audit and onboard Unsealed" and the cause, not only the agent and a generic group

#### Scenario: Several tasks of one agent and reason
- **WHEN** an escalation joins a topic that already holds another task's escalation of the same agent and reason
- **THEN** the topic SHALL be titled with the agent and the group, and each item's note SHALL name its task and cause
