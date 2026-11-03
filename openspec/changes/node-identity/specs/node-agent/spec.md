## MODIFIED Requirements

### Requirement: One node process with worker slots
Each machine SHALL run one node process that registers with Core under a boot epoch and executes up to its configured number of runs concurrently in worker slots, using one Core connection and one NATS connection, both authenticated with the node's keys.

#### Scenario: Relative paths in node.yaml
- **WHEN** node.yaml names `state_dir: data/factory-node` (or another relative path: worktrees root, keys, shim, NATS files) and the node starts from the checkout
- **THEN** the node SHALL resolve each to an absolute path against its working directory at config load, so a conversation run's directory and every path derived from them are absolute

#### Scenario: Node restart
- **WHEN** a node restarts with a new epoch while runs of the previous epoch were unended
- **THEN** the node SHALL seal their worktrees before registering, and Core SHALL end those runs as `lost` without counting them against the daily budget
