## ADDED Requirements

### Requirement: One event feed
Core SHALL record automatic events (exits, authority actions, escalations, run outcomes and retries, questions reaching the owner, approvals, node health, quota pauses) and agents' significant reports in one feed, in the same transaction as their cause.

#### Scenario: Failed attempt
- **WHEN** a run fails and is scheduled for retry
- **THEN** the feed SHALL contain one event naming the task, the outcome and the retry time

#### Scenario: Restart loop
- **WHEN** Core emits `node.restart_loop`
- **THEN** the feed row SHALL be marked as needing attention and SHALL reach Telegram

### Requirement: Reporting policy never blocks
Mandatory reports SHALL be defined by reporting policy rules. A missing mandatory report SHALL produce a feed event for the agent's manager and a reminder in the agent's next brief, and SHALL NOT refuse an exit.

#### Scenario: Done without the required report
- **WHEN** an agent calls `factory_exit_done` without the report its policy requires
- **THEN** the exit SHALL be accepted and the manager SHALL see a missing-report event

### Requirement: Filtered delivery and visibility
Feed events SHALL be delivered through the outbox to NATS subjects that subscribers filter. Managers SHALL see only their subtree's events and the owner SHALL see all.

#### Scenario: Lead lists events
- **WHEN** a lead lists the feed through MCP
- **THEN** only events of tasks assigned within or delegated by its subtree SHALL be returned

### Requirement: Telegram only for attention and digest
The Telegram channel SHALL send only attention-needed events (a question or escalation that reached the owner, a node in trouble, a report needing owner action), reminders for pending owner questions and one daily digest. Replies SHALL answer the question they refer to exactly once.

#### Scenario: Routine handoff
- **WHEN** a developer submits work to a reviewer
- **THEN** no Telegram message SHALL be sent and the event SHALL appear in `watch` and `log`

#### Scenario: Duplicate Telegram update
- **WHEN** the same Telegram update is received twice
- **THEN** the answer SHALL be applied once

#### Scenario: Addressed owner message
- **WHEN** the owner sends `@B please re-check the release notes` in Telegram
- **THEN** it SHALL become owner input on direct report B's conversation task, and an unaddressed message SHALL go to the default direct report
