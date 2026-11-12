## ADDED Requirements

### Requirement: Cloud connectors
A company SHALL be able to run its agents on a connector App Factory hosts instead of its own machine; the cloud
connector SHALL behave as any connector and SHALL be suspended while it has no work.

#### Scenario: Starting without a machine
- **WHEN** a new company chooses "Run in App Factory cloud" in the setup wizard
- **THEN** a cloud connector is created for it and its agents start working without anything installed

### Requirement: Cloud usage paid in credits, separate from own connectors
Work on cloud connectors SHALL be charged in credits at our cost times a markup and shown per agent; credits and
cloud budgets SHALL never limit or charge work on the company's own connectors; an empty balance or a used-up cloud
budget SHALL make new cloud work wait with a reason, never lose it.

#### Scenario: Overflow with the cloud budget used up
- **WHEN** a team works "your machines first, cloud when busy" and today's cloud budget is used up
- **THEN** new work waits for the team's own machines with that reason, and nothing is charged

#### Scenario: Own machine only
- **WHEN** a team works on "your machines only" and the company has no credits
- **THEN** its work runs on its connectors exactly as before and no credit limit is shown for it

### Requirement: Virtual nodes are isolated from Core
All management and configuration of virtual nodes SHALL live in the virtual nodes manager, which SHALL talk to Core
only through Core's public API; Core SHALL treat a virtual node as an ordinary node and SHALL contain no notion of a
virtual node, a provider, credits or cloud budgets.

#### Scenario: Manager not deployed
- **WHEN** App Factory runs without the virtual nodes manager
- **THEN** Core, nodes and the web work as before, and existing virtual nodes appear as nodes that are offline

#### Scenario: Virtual node claims a run
- **WHEN** a virtual node is awake and a run is claimable for it
- **THEN** it enrolls, claims and reports through the same node API, keys and signatures as any node
