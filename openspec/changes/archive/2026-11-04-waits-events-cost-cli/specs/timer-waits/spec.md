## ADDED Requirements

### Requirement: Timer-only waits
An agent SHALL wait for the outside world only by ending its run with `factory_exit_wait` carrying a reason, a wake time and a deadline. The wait SHALL be a park with a delayed `wait_ended` trigger stored in the database, with no probe or worker job.

#### Scenario: Waiting for store processing
- **WHEN** an agent waits 30 minutes for a build to be processed
- **THEN** no run SHALL exist for the task during the wait and a new run SHALL start after the wake time with `wait_ended` in its brief

### Requirement: Wait limits
A wait SHALL be refused when its interval is shorter than the configured minimum or it has no deadline. Consecutive waits without progress SHALL NOT extend past the first deadline and SHALL be limited in number; reaching the deadline or the limit SHALL escalate to the assignee's manager.

#### Scenario: Too short
- **WHEN** an agent asks to be woken in five minutes with a 15-minute minimum
- **THEN** Core SHALL refuse the exit with the minimum in the message

#### Scenario: Deadline reached
- **WHEN** a task is still waiting when its deadline passes
- **THEN** Core SHALL open a `wait_deadline` escalation to the manager with extend, retry, reassign and cancel

### Requirement: Early wake by authority input
Authority input SHALL end an active wait immediately and make the task runnable.

#### Scenario: Manager changes plan during a wait
- **WHEN** a manager directs a waiting task
- **THEN** the wait SHALL end as interrupted and the task SHALL run without waiting for the timer
