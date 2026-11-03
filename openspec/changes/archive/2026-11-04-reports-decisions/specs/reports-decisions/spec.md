## ADDED Requirements

### Requirement: Significant reports
Significant reports SHALL persist purpose, changes, result, owner action and scoped references, with stable request retry and immutable content. Owner notifications SHALL NOT block a task or confer approval.

#### Scenario: Retried report
- **WHEN** an agent repeats a report with the same request key
- **THEN** Core SHALL return the original report unchanged

### Requirement: Automatic outcome records
Observable terminal outcomes SHALL be automatically recorded and deduplicated; UNKNOWN reconciliation SHALL remain explicit.

#### Scenario: Run outcome unknown
- **WHEN** a run ends UNKNOWN
- **THEN** no outcome SHALL be recorded until it is reconciled explicitly

### Requirement: Proposals and approved decisions
Proposals and approved decisions SHALL remain distinct; chat and task review SHALL NOT approve them. Concurrent decision updates SHALL use expected versions, and a replacement SHALL explicitly supersede the current approved choice atomically.

#### Scenario: Stale update
- **WHEN** two updates of one decision are sent with the same expected version
- **THEN** only the first SHALL apply

### Requirement: Owner-authorized agent decisions
Agent decision recording SHALL require exact owner authorization and SHALL NOT bypass denies.

#### Scenario: Agent records without authorization
- **WHEN** an agent records a decision without an exact owner authorization
- **THEN** Core SHALL refuse it

### Requirement: Decision context for runs
New runs SHALL receive bounded current approved context; existing run context SHALL remain unchanged.

#### Scenario: Decision approved during a run
- **WHEN** a decision is approved while a run is active
- **THEN** the active run's context SHALL stay as it was and the next run SHALL receive the decision

### Requirement: Durable journal access
Owner API/CLI, scoped MCP and a paginated project feed SHALL expose the durable journal across restart.

#### Scenario: Journal after restart
- **WHEN** Core restarts
- **THEN** the owner SHALL read the same reports and decisions page by page
