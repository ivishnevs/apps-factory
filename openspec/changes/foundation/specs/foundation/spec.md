# Foundation requirements

## ADDED Requirements

### Requirement: Single writer and no blind retry
Core SHALL atomically enqueue a run and outbox, allow one writer per project, and
claim a run once. Lost leases SHALL become UNKNOWN without automatic rescheduling.

#### Scenario: Duplicate delivery
- **WHEN** a claimed command is delivered again
- **THEN** it SHALL be acknowledged without starting another runtime

### Requirement: Official RPC
Worker SHALL initialize app-server, start/resume a thread, start/interrupt a turn,
handle notifications and server requests, and preserve thread IDs and terminal results.

#### Scenario: Successful run
- **WHEN** a turn completes
- **THEN** it SHALL produce durable events and a COMPLETED SQLite run with final output
