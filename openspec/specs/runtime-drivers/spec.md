# runtime-drivers Specification

## Purpose
Run agents through a runtime-neutral driver interface with Codex and Claude implementations.

## Requirements

### Requirement: Runtime-neutral driver interface
The node SHALL run agents only through a driver interface that declares capabilities and emits normalized events for session, activity, usage, cost, result, quota, authentication errors, turn limits and classified errors. An agent option a driver does not support SHALL be rejected at configuration load and SHALL NOT be silently dropped.

#### Scenario: Browser requested on a runtime without it
- **WHEN** an agent requests a browser on a runtime whose driver lacks the browser capability
- **THEN** configuration validation SHALL fail with the agent and option named

#### Scenario: Effort and turn limit
- **WHEN** an agent sets `runtime.effort` and, on Claude, `runtime.max_turns`
- **THEN** the run's `runtime_options` SHALL carry them (Codex sends effort with `turn/start`, Claude passes `effort` and `maxTurns`), an unknown effort, a `max_turns` below 1 or `max_turns` on Codex SHALL fail validation, and unset values SHALL be omitted

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

#### Scenario: Probe killed by its timeout on a loaded machine
- **WHEN** a driver's probe is killed by its timeout, returns no output or cannot start for lack of resources
- **THEN** the node SHALL retry it with a longer timeout, SHALL NOT disable the driver unless the configured number of consecutive checks (default 3) fail that way, SHALL log at most a warning and SHALL NOT emit `driver_unsupported` for it

#### Scenario: Transient probe failure while runs succeed
- **WHEN** a run on the driver succeeded or produced runtime activity since the last check and the probe then fails transiently
- **THEN** the driver SHALL stay enabled; a definitive failure (not logged in, unsupported version, missing binary) SHALL still disable it at once

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

#### Scenario: Developer resumes after a Codex review
- **WHEN** a Claude developer's task returns to it after a Codex reviewer's run
- **THEN** the developer's run SHALL carry the developer's last Claude session of the task, not the reviewer's Codex thread

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

### Requirement: Factory MCP tools load with their schemas
The Claude driver SHALL configure the Factory MCP server so its tools reach the model with their full input schemas from the first turn, never deferred behind Claude Code's MCP tool search. It SHALL NOT disable tool search for the whole run: other deferrable tools keep Claude Code's default behaviour, which `ENABLE_TOOL_SEARCH` in the run environment still governs.

#### Scenario: Claude run starts
- **WHEN** the shim starts a Claude run with Factory MCP
- **THEN** the SDK options SHALL declare the `factory` stdio server with `alwaysLoad: true`, and the shim SHALL NOT set `ENABLE_TOOL_SEARCH`

### Requirement: Driver state for the owner
Each driver a node declares in register and heartbeat SHALL carry, besides `ok`, `check` and `detail`, `transient_failures` (checks in a row that failed only transiently while the driver stays as it was) and `until` (the end of a run's auth failure hold-off, Unix seconds). Core SHALL keep them with the node's drivers and return them in `GET /nodes`.

#### Scenario: Auth hold-off
- **WHEN** a run on the claude driver ends on the runtime's authentication failure
- **THEN** the next heartbeat SHALL declare claude not ok with check `auth` and `until` the end of its hold-off, and `GET /nodes` SHALL show it

#### Scenario: Transient failure
- **WHEN** a driver's check fails only transiently for the second time in a row and the limit is three
- **THEN** the driver SHALL stay ok with `transient_failures` 2

### Requirement: Disk watermarks in node health
The node's heartbeat health SHALL carry the disk watermarks it judges `disk` by (`disk_low_bytes`, `disk_critical_bytes`).

#### Scenario: Disk watermarks
- **WHEN** the node reports its health
- **THEN** it SHALL include the low and critical watermarks of its disk path

### Requirement: Node-owned runtime logins
A node SHALL run Claude Code and Codex with configuration and credential directories of its own, separate from the
machine user's, logged in through `factory-node login`, so that the person logging in or out in their own terminal
does not change the account the agents use.

#### Scenario: Person switches their terminal to a work account
- **WHEN** the person logs their own Claude Code in to a work subscription
- **THEN** the node's runs SHALL keep using the account the node was logged in with

### Requirement: Account change stops the runtime
A node SHALL record which account each runtime uses (plan, organisation, a hash of the email) and check it before
each run. A different account or a logout SHALL pause that runtime on the node, keep its runs queued, and tell the
owner which account it is now, with a choice to continue on it or keep it paused.

#### Scenario: Account changed
- **WHEN** the Claude account of a node changes from a personal plan to an organisation's plan
- **THEN** no Claude run SHALL start on that node until the owner continues, and the owner SHALL get an Inbox item
  naming the new plan and organisation

### Requirement: Sessions do not cross accounts
A node SHALL record the runtime account each session ran under and SHALL NOT resume a session under a different
account. Such a run SHALL start a fresh session whose prompt says the context comes from Core, as on a new node.
When the owner continues on a changed account, the node SHALL treat all of that runtime's sessions as belonging to
the previous account.

#### Scenario: Account changed and accepted
- **WHEN** a node's Claude login changes, the owner continues on the new account, and the next run of a task that
  had a session on that node starts
- **THEN** the run SHALL start a fresh session with the Core-context note and SHALL NOT resume the old session

### Requirement: A lost session asks the owner
When a node recorded a session for a task and agent, the account is unchanged, and the runtime no longer has that
session, the node SHALL NOT start the run and SHALL report `session_lost`. Core SHALL open one escalation per task
and agent asking the owner to continue with a new session or wait. A task that moved to another node SHALL NOT
cause this escalation.

#### Scenario: Transcript deleted
- **WHEN** the transcript of a task's session is deleted on the node that ran it and the task's next run is placed
  there
- **THEN** the owner SHALL get an Inbox item with Continue and Wait, and no run SHALL start until they answer

#### Scenario: Owner continues
- **WHEN** the owner answers Continue
- **THEN** the run SHALL be queued again and start a fresh session with the Core-context note
