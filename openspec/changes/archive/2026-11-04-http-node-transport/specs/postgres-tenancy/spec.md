## ADDED Requirements

### Requirement: Postgres as the only Core store and source of node work
Core SHALL persist all factory state in Postgres and SHALL keep idempotent mutation replay and optimistic versions. Work for nodes SHALL be read from Postgres state (queued runs, stops, resets, cleanups, sync jobs), not from a message stream.

#### Scenario: Mutation and its effects
- **WHEN** a mutation that queues a run commits
- **THEN** the run SHALL be visible to the offers of capable nodes of its company without any separate message

#### Scenario: Core restarts with queued runs
- **WHEN** Core restarts while runs are queued
- **THEN** nodes SHALL get them as offers from their next poll without any republishing

## MODIFIED Requirements

### Requirement: Company-scoped credentials and subjects
Run credentials SHALL be bound to the run's company, and every node route SHALL be bound to the node's company.

#### Scenario: Foreign run token
- **WHEN** an MCP call presents a run token of company A for a task of company B
- **THEN** Core SHALL refuse it as not found and record the refusal in the tool audit

#### Scenario: Foreign node
- **WHEN** a node of company A polls, claims or sends events for work of company B
- **THEN** Core SHALL answer not found

## REMOVED Requirements

### Requirement: Postgres as the only Core store
**Reason**: The transactional outbox and its NATS publishing are removed (`http-node-transport`); replaced by "Postgres as the only Core store and source of node work".
**Migration**: None (not released).
