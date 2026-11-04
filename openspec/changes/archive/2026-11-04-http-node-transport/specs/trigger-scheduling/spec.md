## ADDED Requirements

### Requirement: Trigger log as the only wake-up
Every reason to run a task SHALL be recorded as a trigger row in the same transaction as its cause, and SHALL raise the task's `wake_seq`. A successful run SHALL advance `handled_seq` to the highest trigger it was shown. An agent's own comment SHALL NOT trigger its own task.

#### Scenario: Handoff while Core restarts
- **WHEN** a task is assigned to another agent and Core restarts before the dispatcher runs
- **THEN** the assignment trigger SHALL be committed and the task SHALL be dispatched once the dispatcher runs, without any separate wake message

#### Scenario: Last dependency closes
- **WHEN** the last open dependency of a task becomes DONE
- **THEN** exactly one `dependency_done` trigger SHALL be added to the dependent task

### Requirement: Dispatcher and queue reconciler
The dispatcher SHALL create a queued run for each runnable task without a live run, and nodes SHALL take queued runs from their offers. The reconciler SHALL end queued runs older than the queue TTL without counting an attempt.

#### Scenario: Core restored from an older backup
- **WHEN** Core's database is restored while runs are QUEUED
- **THEN** capable nodes SHALL get those runs as offers and each run SHALL execute at most once

#### Scenario: Nobody can claim
- **WHEN** a queued run is not claimed within the queue TTL
- **THEN** the run SHALL end as `unclaimed` without an attempt and the task SHALL be dispatched again with reason `no_capable_node` visible while unclaimed

## MODIFIED Requirements

### Requirement: Event feed table
Core SHALL record scheduling events in one `feed_events` table in the transaction of their cause.

#### Scenario: Rolled-back dispatch
- **WHEN** a dispatch transaction rolls back
- **THEN** no feed row and no queued run SHALL exist for it

## REMOVED Requirements

### Requirement: Durable trigger log
**Reason**: Its scenario relied on the NATS bus (`http-node-transport`); replaced by "Trigger log as the only wake-up" with the same rules.
**Migration**: None (not released).

### Requirement: Dispatcher and reconciler
**Reason**: Commands are no longer published or republished (`http-node-transport`); replaced by "Dispatcher and queue reconciler".
**Migration**: None (not released).
