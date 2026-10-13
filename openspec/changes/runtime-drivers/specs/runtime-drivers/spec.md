## ADDED Requirements

### Requirement: Runtime-neutral driver interface
The node SHALL run agents only through a driver interface that declares capabilities and emits normalized events for session, activity, usage, cost, result, quota, authentication errors, turn limits and classified errors. An agent option a driver does not support SHALL be rejected at configuration load and SHALL NOT be silently dropped.

#### Scenario: Browser requested on a runtime without it
- **WHEN** an agent requests a browser on a runtime whose driver lacks the browser capability
- **THEN** configuration validation SHALL fail with the agent and option named

### Requirement: Codex and Claude drivers
The factory SHALL provide a Codex app-server driver and a Claude driver managed by the same node process, with runtime and model chosen per agent. The Claude driver SHALL run the Agent SDK in a per-run child process and SHALL NOT implement claim, lease or heartbeat logic. Both drivers SHALL use the shared MCP adapter, exit guard, command guards and cost accounting.

#### Scenario: Claude run stopped by the node
- **WHEN** the node interrupts a Claude run and the SDK returns a result with an aborted terminal reason
- **THEN** the driver SHALL report the result as interrupted with the usage last reported, and the child process SHALL exit

#### Scenario: Mixed runtimes on one task
- **WHEN** a Claude Code developer submits a task to a Codex reviewer
- **THEN** both runs SHALL use the same MCP tools and exits, and both SHALL have usage recorded

### Requirement: Supported version ranges
Each driver SHALL declare a supported runtime version range checked at node start and periodically. A runtime outside the range SHALL disable that driver on the node with one alert and SHALL NOT crash the node.

#### Scenario: Runtime upgraded outside the range
- **WHEN** the installed runtime version is newer than the supported range
- **THEN** the node SHALL register without that driver, Core SHALL emit one `driver_unsupported` event, and tasks needing it SHALL report `no_capable_node`

#### Scenario: Authentication probe fails
- **WHEN** a driver's authentication probe fails on a node with another usable driver
- **THEN** only that driver SHALL be disabled with one `driver_unsupported` event, and it SHALL be re-enabled when the probe passes

### Requirement: Codex command guards
Command guards SHALL apply to Codex runs through PATH shims and by answering the app-server's approval requests according to the agent's policies.

#### Scenario: Denied command through an approval request
- **WHEN** Codex requests approval to execute a command denied by policy
- **THEN** the driver SHALL decline it with the rule's instruction and record a refusal event

### Requirement: Usage on the run and session keys
The node SHALL report the last known usage with the run end, and Core SHALL store it on the run. Sessions SHALL be keyed by task and agent and SHALL NOT be resumed on a different node.

#### Scenario: Interrupted run
- **WHEN** a run is revoked after consuming tokens
- **THEN** the run record SHALL contain the usage last reported for it

### Requirement: Claude subscription limits are quota events
A Claude rate-limit event with status rejected SHALL be reported as a quota event carrying its reset time and SHALL NOT be reported as a failed attempt.

#### Scenario: Five-hour limit reached
- **WHEN** the SDK emits a rate-limit event with status rejected and a reset time during a run
- **THEN** the run SHALL end `paused_out` and the agent's quota key SHALL be paused until that reset time

### Requirement: Quota keys per company
Agents SHALL reference a quota key of their company. A quota limit SHALL end the run as `paused_out` without an attempt and SHALL pause only the agents using that key until its reset time.

#### Scenario: Limit reached on one subscription
- **WHEN** a run on quota key A reports a usage limit with a reset time
- **THEN** agents on key A SHALL show `paused:quota_key` until the reset, and agents on key B SHALL keep running
