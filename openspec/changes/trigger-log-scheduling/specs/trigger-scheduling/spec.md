## ADDED Requirements

### Requirement: Durable trigger log
Every reason to run a task SHALL be recorded as a trigger row in the same transaction as its cause, and SHALL raise the task's `wake_seq`. A successful run SHALL advance `handled_seq` to the highest trigger it was shown. An agent's own comment SHALL NOT trigger its own task.

#### Scenario: Handoff while the bus is down
- **WHEN** a task is assigned to another agent while NATS is unavailable
- **THEN** the assignment trigger SHALL be committed and the task SHALL be dispatched once the dispatcher runs, without any separate wake message

#### Scenario: Last dependency closes
- **WHEN** the last open dependency of a task becomes DONE
- **THEN** exactly one `dependency_done` trigger SHALL be added to the dependent task

### Requirement: Explicit parks
Automatic holds (questions, pending approvals, manual blocks, children, waits, escalations, inspections) SHALL be explicit park records with a kind, a reference and the `wake_seq` at creation. BLOCKED SHALL be derived from active parks, not stored.

#### Scenario: Blocking question
- **WHEN** an agent asks a blocking question for its task
- **THEN** the task SHALL get an active `ask` park and report status BLOCKED with reason `parked:ask`

### Requirement: Runnable predicate with a single reason
Core SHALL decide whether a task may run with one pure function over a snapshot that returns runnable, a reason and a clearer. The reason SHALL be the first false condition in the order: terminal, backlog, unassigned, live_run, parked (escalation, inspect, wait, ask, block, children), dependencies, artifacts, paused, backoff, idle, pinned_node_unavailable, no_capable_node, capacity. The scheduler, claim and owner CLI SHALL use the same function.

#### Scenario: Idle task
- **WHEN** a READY task has `wake_seq` equal to `handled_seq` and nothing else holds it
- **THEN** it SHALL NOT be dispatched and its reason SHALL be `idle`

#### Scenario: Escalation and wait together
- **WHEN** a task has an active wait park and an active escalation park
- **THEN** its reason SHALL be `parked:escalation`

#### Scenario: Parked on children
- **WHEN** a task is parked on its open children
- **THEN** its clearer SHALL name the children's assignees (`agent:<name>`, or `children` when unassigned) and the action to wait for the named children to close or to release it early with `factory tasks ready`, not the owner

#### Scenario: Waiting for a worktree reset
- **WHEN** a task's block park waits for its node to reset a diverged worktree
- **THEN** its clearer SHALL be `node`

### Requirement: Dispatcher and reconciler
The dispatcher SHALL create a run and an outbox command for each runnable task without a live run. The reconciler SHALL republish commands for queued runs that were not claimed within the redelivery interval and SHALL end queued runs older than the queue TTL without counting an attempt.

#### Scenario: Restored stream
- **WHEN** the command stream is purged or restored from an older backup while runs are QUEUED
- **THEN** the reconciler SHALL publish new commands for those runs and each run SHALL execute at most once

#### Scenario: Nobody can claim
- **WHEN** a queued run is not claimed within the queue TTL
- **THEN** the run SHALL end as `unclaimed` without an attempt and the task SHALL be dispatched again with reason `no_capable_node` visible while unclaimed

### Requirement: Claim re-check
Claim SHALL re-evaluate the run and its task against the database and SHALL return the RunSpec with a brief of the triggers in `(handled_seq, wake_seq]`. A stale claim SHALL end the run as `stale` without an attempt and tell the worker to drop the command.

#### Scenario: Reassigned before claim
- **WHEN** a worker claims a run whose task was reassigned after dispatch
- **THEN** Core SHALL answer 409 with `drop: true`, the run SHALL end as `stale`, and no runtime SHALL start

#### Scenario: Manager already running
- **WHEN** a run of a manager agent with the default cap is claimed while another run of the same agent is live
- **THEN** Core SHALL answer 409 with `retry: true` and the run SHALL stay QUEUED

### Requirement: Brief carries context, rules and facts
The brief SHALL contain the previous run's outcome, the triggers since `handled_seq` with author and addressee, comments since the last run, tasks created by earlier runs of the task, the branch log, and the company and project rules and facts. Agents SHALL also be able to read the rules and facts through `factory_settings_get`.

#### Scenario: Company rule changed
- **WHEN** the owner changes a company rule and a task is claimed afterwards
- **THEN** the run's brief SHALL contain the new rule

### Requirement: Deduplicated agent-created tasks
A task created by an agent SHALL record its origin task. Creating a task while an open task with the same origin, normalized title and assignee role exists SHALL return the existing task. Creating a task whose parent and dependencies form a cycle SHALL be refused.

#### Scenario: Agent retries task creation
- **WHEN** an agent creates "Fix login" from task T twice
- **THEN** only one task SHALL exist and the second call SHALL return it

### Requirement: Event feed table
Core SHALL record scheduling events in one `feed_events` table in the transaction of their cause and SHALL publish each row through the outbox to NATS.

#### Scenario: Rolled-back dispatch
- **WHEN** a dispatch transaction rolls back
- **THEN** no feed row and no outbox message SHALL exist for it
