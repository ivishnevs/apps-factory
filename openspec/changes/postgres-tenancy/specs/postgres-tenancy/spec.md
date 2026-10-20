## ADDED Requirements

### Requirement: Postgres as the only Core store
Core SHALL persist all factory state in Postgres and SHALL keep the transactional outbox, idempotent mutation replay and optimistic versions with the same observable behaviour as the SQLite implementation.

#### Scenario: Mutation with outbox
- **WHEN** a mutation commits
- **THEN** its outbox rows SHALL be committed in the same transaction and published to NATS at least once with a stable message ID

#### Scenario: Crash before marking sent
- **WHEN** Core stops after a JetStream ack but before marking the outbox row sent
- **THEN** the row SHALL be republished with the same message ID and consumers SHALL apply it at most once

### Requirement: Account and company tenancy
Core SHALL model accounts that own companies, and users that are members of one or more companies of their account. Agents, projects, policies, quota keys, nodes and all derived records SHALL belong to exactly one company.

#### Scenario: User in two companies
- **WHEN** a user who is a member of companies A and B calls the owner API with a token for company A
- **THEN** Core SHALL return and modify only company A's data

### Requirement: Company key on every row
Every table SHALL carry `company_id`, and every foreign key SHALL include `company_id`, so that no row can reference a row of another company.

#### Scenario: Cross-company reference
- **WHEN** a write would link a task of company A to a project of company B
- **THEN** the database SHALL reject the write and Core SHALL return an error without partial effects

### Requirement: Company-scoped credentials and subjects
Run credentials SHALL be bound to the run's company, and NATS subjects SHALL include the company identifier.

#### Scenario: Foreign run token
- **WHEN** an MCP call presents a run token of company A for a task of company B
- **THEN** Core SHALL refuse it as not found and record the refusal in the tool audit

### Requirement: Serialized decisions per company
Transactions that read state and decide on it (dispatch, claim, lease expiry, approval expiry) SHALL serialize per company, and different companies SHALL NOT block each other.

#### Scenario: Concurrent dispatch and claim
- **WHEN** dispatch, claim and expiry run concurrently for the same company
- **THEN** a task SHALL never have more than one live run

### Requirement: Versioned forward-only migrations
Core SHALL apply embedded numbered migrations exactly once under a startup lock and SHALL refuse to start on a database with an unknown newer version.

#### Scenario: Two Core processes start
- **WHEN** two Core processes start against an empty database
- **THEN** each migration SHALL be applied exactly once

#### Scenario: Newer database
- **WHEN** the database has a migration version newer than Core knows
- **THEN** Core SHALL refuse to start
