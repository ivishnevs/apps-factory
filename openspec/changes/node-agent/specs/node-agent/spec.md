## ADDED Requirements

### Requirement: One node process with worker slots
Each machine SHALL run one node process that registers with Core under a boot epoch and executes up to its configured number of runs concurrently in worker slots, using one Core connection and one NATS connection.

#### Scenario: Node restart
- **WHEN** a node restarts with a new epoch while runs of the previous epoch were unended
- **THEN** the node SHALL seal their worktrees before registering, and Core SHALL end those runs as `lost` without counting them against the daily budget

### Requirement: Inventory heartbeat
The node SHALL heartbeat with the inventory of its runs and its health. Core SHALL renew the leases of listed runs, SHALL mark lost only running runs missing from the inventory, and SHALL declare a node offline only after it has been silent for the offline interval of continuous Core operation.

#### Scenario: Run missing from inventory
- **WHEN** a node's heartbeat no longer lists a RUNNING run
- **THEN** Core SHALL end that run as `lost` and include no other run of the node

#### Scenario: Run unknown after a database restore
- **WHEN** a node's inventory lists a run id that Core does not know
- **THEN** the heartbeat answer SHALL list it under `stop` and the node SHALL stop and seal it

### Requirement: Server incarnation
Core SHALL expose an incarnation identifier that changes when its database is restored. A node that observes a new incarnation SHALL stop and seal all its runs and re-register with a new epoch.

#### Scenario: Restore from backup
- **WHEN** the owner restores Core's database from a backup while a node has live runs
- **THEN** the node SHALL stop those runs, re-register, and the tasks SHALL run again from the restored triggers

### Requirement: Per-node control subject
The node SHALL subscribe to `factory.<company>.nodes.<node_id>.control` and SHALL stop a run on a `run.stop` message addressed to it, independently of the work-queue command stream.

#### Scenario: Stop while slots are busy
- **WHEN** all slots are busy and a stop for one of its runs is published
- **THEN** the node SHALL receive it immediately and stop that run

### Requirement: Resource classes respected at claim
Nodes SHALL declare capacities per resource class and agents SHALL declare their needs. Core SHALL grant a claim only when the node has a free slot, the agent's labels and driver, and enough capacity in every class, and SHALL release capacity only after the run has ended and been sealed.

#### Scenario: One simulator slot
- **WHEN** two agents that each need one simulator become runnable on a node with simulator capacity 1
- **THEN** only one run SHALL be claimed and the other task SHALL report reason `capacity` and stay queued until the first run is sealed

### Requirement: Janitor and disk thresholds
The node SHALL remove worktrees of closed tasks after sealing, SHALL run the company's cleanup hooks, SHALL prune caches by age, and SHALL emit one low-disk alert and clean aggressively below the low threshold and stop claiming below the critical threshold.

#### Scenario: Disk falls below the low threshold
- **WHEN** free disk space drops below the low watermark
- **THEN** the node SHALL prune caches of closed tasks immediately and Core SHALL emit exactly one `node.low_disk` attention event until space recovers

### Requirement: Host UI forbidden by policy
Command-level access rules SHALL be enforced on the node with refusing shims placed first on the agent's PATH, and approval-required commands SHALL pass only once per exact approved invocation. The default catalog SHALL deny host-input tools.

#### Scenario: Agent tries to click
- **WHEN** an agent runs `cliclick` during a run
- **THEN** the command SHALL fail with the policy's instruction and a refusal event SHALL be recorded on the run

#### Scenario: Agent tries the owner CLI
- **WHEN** an agent runs `factory` during a run
- **THEN** the command SHALL fail with an instruction to use the Factory MCP tools

### Requirement: Node health in the event feed
Core SHALL emit node registration, offline, online, low-disk, critical-disk, pause, restart-loop, failed-check and stalled-heartbeat events into the event feed, marking the trouble events as needing attention.

#### Scenario: Laptop lid closed
- **WHEN** a node stops heartbeating while Core keeps running
- **THEN** Core SHALL emit `node.offline` after the offline interval and `node.online` on its next heartbeat

### Requirement: Node supervision and restart loops
The node process SHALL run under the OS service manager with automatic restart, and Core SHALL emit one alert when a
node registers more than 3 new epochs within 15 minutes.

#### Scenario: Crash loop
- **WHEN** the node process crashes four times in ten minutes
- **THEN** it is restarted each time and the owner receives exactly one restart-loop alert

### Requirement: Internal watchdog
The node SHALL periodically verify that every busy slot has a live agent process group and every agent process belongs
to a slot, SHALL kill orphaned agent processes of its runs, and SHALL detect when its heartbeats stop leaving.

#### Scenario: Agent process killed externally
- **WHEN** an agent process of a running slot is killed outside the node
- **THEN** the watchdog ends that run with the matching outcome, seals its worktree and frees the slot

### Requirement: Self-checks and automatic pause
The node SHALL run self-checks on start and periodically — git and forge credentials, Core and NATS connectivity, free
disk, stack checks from template hooks, and the runtime probes of its drivers — emitting one event per failing check. A
failing runtime probe SHALL disable only that driver. The node SHALL pause itself only while no driver remains usable or
another check that prevents all work fails; the pause SHALL clear when the check passes again.

#### Scenario: One runtime logged out
- **WHEN** the authentication probe of one driver fails on a node that also has another usable driver
- **THEN** only that driver is disabled, the node keeps claiming runs for the other driver, and the owner gets one alert

#### Scenario: No usable driver
- **WHEN** every driver on a node fails its probe
- **THEN** the node is paused, Core stops granting it claims, and after the probes pass the node resumes on its own

### Requirement: Doctor command
`factory node doctor` SHALL run all self-checks locally and print a pass/fail table with remediation hints, without
requiring Core.

#### Scenario: Diagnose a new machine
- **WHEN** the owner runs `factory node doctor` on a machine with an outdated Codex version
- **THEN** the table shows the runtime check failed with the supported range and how to update
