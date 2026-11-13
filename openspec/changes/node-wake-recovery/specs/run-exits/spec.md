## MODIFIED Requirements

### Requirement: Exit only from the current live run
Core SHALL refuse an exit with 410 and no state change when the calling run is not the task's current live run, except a late exit: a run that is ENDED with outcome `lost` or `timed_out`, still unreleased, and still the task's current run SHALL have its exit applied. Core SHALL check these conditions in the same transaction that applies the exit. An accepted late exit SHALL end the run `succeeded`, release it and schedule no retry. Only the exit path SHALL be open to such a run; other tools and the run credential endpoint SHALL refuse it.

#### Scenario: Exit after urgent stop
- **WHEN** a revoked run calls `factory_exit_done`
- **THEN** Core SHALL answer 410 and the task SHALL be unchanged

#### Scenario: Late exit of a lost run
- **WHEN** Core marked a run lost while its node was unreachable, the run is still unreleased, and its node delivers the run's handoff exit
- **THEN** Core SHALL apply the exit, the run SHALL end `succeeded` and released, and the task SHALL move on without a retry

#### Scenario: Late exit after release
- **WHEN** a lost run was already released and its task claimed again, and the old run's exit arrives
- **THEN** Core SHALL answer 410 and the task SHALL be unchanged

### Requirement: Exit delivery when Core is unreachable
The adapter SHALL write a pending exit file before it sends an exit and remove it after Core's answer. When Core is unreachable, an exit tool SHALL tell the runtime the exit is queued and to stop working, and the adapter and then the node SHALL retry the exit with the same key until it is accepted, finally refused (Node delivery refusals), or the retry horizon passes. When Core stops a run whose exit is undelivered, the node SHALL stop the runtime, deliver the exit with a bounded context of its own, and only then send the terminal event. A process that ends with an undelivered exit SHALL make the run `lost`; one whose retried exit was refused with 409 SHALL make it `no_exit`.

#### Scenario: Directive arrives while the exit is queued
- **WHEN** the adapter retries a queued exit after the process ended and Core refuses it with 409 because of a new directive
- **THEN** the run outcome SHALL be `no_exit` and the next brief SHALL contain the directive

#### Scenario: Stop arrives before the exit
- **WHEN** Core's `stop: terminal` reaches the node while the run's finished exit is still pending
- **THEN** the node SHALL deliver the exit before the terminal event, and Core SHALL decide it as a late exit

#### Scenario: Killed mid-request
- **WHEN** the run's process group is killed during `POST /agent/exit`
- **THEN** the pending exit file SHALL remain and the node SHALL deliver it
