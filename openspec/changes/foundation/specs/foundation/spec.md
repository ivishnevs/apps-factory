# Foundation requirements

## Durable execution
### Requirement: Single writer and no blind retry
Core SHALL atomically enqueue a run and outbox, allow one writer per project, and
claim a run once. Lost leases SHALL become UNKNOWN without automatic rescheduling.
#### Scenario: Duplicate delivery
A repeated claimed command is acknowledged without starting another runtime.

## Runtime
### Requirement: Official RPC
Worker SHALL initialize app-server, start/resume a thread, start/interrupt a turn,
handle notifications and server requests, and preserve thread IDs and terminal results.
#### Scenario: Successful run
A completed turn produces durable events and a COMPLETED SQLite run with final output.
