## MODIFIED Requirements

### Requirement: One node process with worker slots
Each machine SHALL run one node process that registers with Core under a boot epoch and executes up to its configured number of runs concurrently in worker slots, using one Core connection authenticated with the node's keys and no other connection.

#### Scenario: Relative paths in node.yaml
- **WHEN** node.yaml names `state_dir: data/factory-node` (or another relative path: worktrees root, keys, shim) and the node starts from the checkout
- **THEN** the node SHALL resolve each to an absolute path against its working directory at config load, so a conversation run's directory and every path derived from them are absolute

#### Scenario: Node restart
- **WHEN** a node restarts with a new epoch while runs of the previous epoch were unended
- **THEN** the node SHALL seal their worktrees before registering, and Core SHALL end those runs as `lost` without counting them against the daily budget

### Requirement: Self-checks and automatic pause
The node SHALL run self-checks on start and periodically — git and forge credentials, Core connectivity, free
disk, stack checks from template hooks, and the runtime probes of its drivers — emitting one event per failing check. A
failing runtime probe SHALL disable only that driver. The node SHALL pause itself only while no driver remains usable or
another check that prevents all work fails; the pause SHALL clear when the check passes again.

#### Scenario: One runtime logged out
- **WHEN** the authentication probe of one driver fails on a node that also has another usable driver
- **THEN** only that driver is disabled, the node keeps claiming runs for the other driver, and the owner gets one alert

#### Scenario: Driver probe slow under load
- **WHEN** a driver probe times out once while the machine is busy and a run on that driver is succeeding
- **THEN** the driver stays enabled, the node logs a warning and keeps claiming its runs, and the owner gets no alert

#### Scenario: No usable driver
- **WHEN** every driver on a node fails its probe
- **THEN** the node is paused, Core stops granting it claims, and after the probes pass the node resumes on its own

## REMOVED Requirements

### Requirement: Per-node control subject
**Reason**: NATS is removed (`http-node-transport`); stops reach the node in its poll answer and, as the guarantee, in the heartbeat answer.
**Migration**: None (not released). The node no longer subscribes to `factory.<company>.nodes.<node_id>.control`.
