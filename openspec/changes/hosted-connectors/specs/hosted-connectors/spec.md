## ADDED Requirements

### Requirement: Cloud connectors
A company SHALL be able to run its agents on a connector App Factory hosts instead of its own machine; the cloud
connector SHALL behave as any connector and SHALL be suspended while it has no work.

#### Scenario: Starting without a machine
- **WHEN** a new company chooses "Run in App Factory cloud" in the setup wizard
- **THEN** a cloud connector is created for it and its agents start working without anything installed

### Requirement: Usage paid from a wallet
Work on cloud connectors SHALL be charged to the company's wallet at our cost plus a margin and booked to the agent
that did it; an empty wallet or a reached salary cap SHALL make new cloud work wait with a reason, never lose it.

#### Scenario: Salary cap reached
- **WHEN** an agent's runs on a cloud connector reach its daily salary cap
- **THEN** its new work waits with a reason naming the cap and the owner can raise the cap from the escalation
