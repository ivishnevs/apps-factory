## ADDED Requirements

### Requirement: Machines are called nodes
User-facing text SHALL call a machine that runs agents a node (and a hosted one a virtual node), never a connector.

#### Scenario: Old link
- **WHEN** someone opens a `/connectors` link in the web
- **THEN** the web SHALL redirect to the matching `/nodes` page
