## ADDED Requirements

### Requirement: Run outcome table
Every ended run SHALL have exactly one outcome from: succeeded, no_exit, failed, timed_out, idle_timeout, max_turns, setup_failed, lost, revoked, paused_out, stale, unclaimed. The outcome SHALL alone determine whether the run counts as an attempt, applies backoff and counts against the daily budget, as defined in the design table.

#### Scenario: Timeout
- **WHEN** a run exceeds its hard timeout
- **THEN** its outcome SHALL be `timed_out`, it SHALL count as an attempt with backoff, and the task SHALL NOT be cancelled

#### Scenario: Owner stop
- **WHEN** the owner stops a running run
- **THEN** its outcome SHALL be `revoked` and SHALL NOT count as an attempt or against the budget

### Requirement: Bounded automatic retry
Core SHALL retry ordinary tasks automatically after an attempt or `lost` outcome, in the same workspace and resuming the same runtime session when supported, after a backoff of 1, 5 and then 15 minutes. Three attempt outcomes in a row without progress for the same assignee SHALL create a `retries_exhausted` escalation; reaching the daily budget SHALL create a `budget_exhausted` escalation.

#### Scenario: Crash then success
- **WHEN** a run fails and the next run of the same task succeeds with progress
- **THEN** the second run SHALL have resumed the first run's session and the counters and backoff SHALL be reset

#### Scenario: Three failures
- **WHEN** three consecutive attempts of the same assignee end without progress
- **THEN** the task SHALL be parked with a `retries_exhausted` escalation offering retry, reassign and cancel

#### Scenario: Budget reached after a directive
- **WHEN** the run that reaches the daily budget ends while the task has authority input above `handled_seq`
- **THEN** no `budget_exhausted` escalation SHALL be opened, today's budget SHALL be reset, and the task SHALL run with that input

### Requirement: Escalation routing before the hierarchy
Until reporting chains exist, escalations SHALL be routed to an agent with role `manager`, or to the owner when the company has none.

#### Scenario: No manager configured
- **WHEN** a task's retries are exhausted in a company without a manager agent
- **THEN** the `retries_exhausted` escalation SHALL reach the owner

### Requirement: Session continuity rules
Runtime sessions SHALL be keyed by (task, agent). A retry on the same node SHALL resume the session; a run on another node or after a workspace reset SHALL start a fresh session with the brief.

#### Scenario: Retry lands on another node
- **WHEN** a failed task is claimed by a different node
- **THEN** the run SHALL start a new session and its brief SHALL state the previous outcome

### Requirement: Stall detection
Core SHALL open a self-clearing `stalled` escalation when a task waits on a task that is in BACKLOG or unassigned, and SHALL emit one alert per episode for tasks idle or without progress longer than the configured intervals.

#### Scenario: Blocked on an unassigned task
- **WHEN** task A is blocked on task B and B has no assignee
- **THEN** A SHALL get a `stalled` escalation naming B, which SHALL clear when B is assigned

#### Scenario: Parked on a child in BACKLOG
- **WHEN** task A is parked on its children and an open child C is in BACKLOG or has no assignee
- **THEN** A SHALL get a `stalled` escalation to its assignee's manager naming C, which SHALL clear when C is assigned and released or closes, and the answer `unblock` SHALL release A from its children park

### Requirement: Per-task inspection for external side effects
For a task marked as having external side effects, any uncertain outcome SHALL park only that task with an `inspect` escalation and SHALL NOT retry automatically. Other tasks, the project and its workspaces SHALL remain schedulable.

#### Scenario: Lost publishing run
- **WHEN** a run of a side-effect task is lost
- **THEN** that task SHALL be parked for inspection with answers retry, continue and cancel, and other tasks of the project SHALL keep being dispatched

#### Scenario: Ordinary task lost
- **WHEN** a run of a task without side effects is lost
- **THEN** the task SHALL become runnable again after backoff without owner action

### Requirement: Two-phase claim
A claim SHALL create a CLAIMED run that the worker MUST start within the start window, and a repeated claim with the same worker, epoch and claim identifier SHALL return the same lease. A CLAIMED run not started within the window SHALL end as `lost`. A newer worker epoch SHALL end the older epoch's unended runs as `lost`.

#### Scenario: Claim response lost
- **WHEN** a worker retries a claim after a timeout with the same claim key
- **THEN** Core SHALL return the same lease and SHALL NOT create a second run

### Requirement: Sleep-safe liveness
Core SHALL NOT expire a lease or start window until it has been ticking continuously for longer than the TTL since the later of the last renewal and the start of its current continuity. Worker timeouts SHALL be measured with a clock that does not advance during system sleep.

#### Scenario: Laptop sleeps for two hours
- **WHEN** the machine running Core and the worker sleeps for two hours and wakes up
- **THEN** no run SHALL be marked lost or timed out because of the sleep, provided its worker heartbeats within one TTL after wake
