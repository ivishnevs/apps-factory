## ADDED Requirements

### Requirement: Runtime network checks
The node's `doctor` and periodic checks SHALL resolve and reach each enabled runtime's API host and the forge through the system resolver. A failure SHALL disable the driver with the cause and the fix. A run that ends with a network error to the API host SHALL count as a node fault, retried on another node and not counted as an attempt without progress.

#### Scenario: Broken DNS on a node
- **WHEN** a node cannot resolve api.anthropic.com
- **THEN** `doctor` SHALL fail `net.api`, the node SHALL take no Claude runs, and the queued tasks SHALL keep their attempts
