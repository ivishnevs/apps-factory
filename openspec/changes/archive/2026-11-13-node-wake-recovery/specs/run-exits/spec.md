## MODIFIED Requirements

### Requirement: Exit only from the current live run
Core SHALL return 410 without state changes unless the caller is the task's current live run or its ENDED, `lost`/`timed_out`, unreleased run. The late-exit check and exit SHALL be transactional. An accepted late exit SHALL mark the run `succeeded`, release it and prevent retries. Only `/agent/exit` SHALL permit that late run; all other tools and the credential endpoint SHALL refuse it.

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
The adapter SHALL persist a pending exit before POST and remove it after Core answers. On outage it SHALL tell the runtime "queued; stop". The adapter, then node, SHALL retry the same key until acceptance, final refusal or retry horizon. On stop, the node SHALL stop the runtime, deliver the exit with a separate bounded context, then send the terminal event. An undelivered exit SHALL end the run `lost`; a retried exit refused with 409 SHALL end it `no_exit`.

#### Scenario: Directive arrives while the exit is queued
- **WHEN** the adapter retries a queued exit after the process ended and Core refuses it with 409 because of a new directive
- **THEN** the run outcome SHALL be `no_exit` and the next brief SHALL contain the directive

#### Scenario: Stop arrives before the exit
- **WHEN** Core's `stop: terminal` reaches the node while the run's finished exit is still pending
- **THEN** the node SHALL deliver the exit before the terminal event, and Core SHALL decide it as a late exit

#### Scenario: Killed mid-request
- **WHEN** the run's process group is killed during `POST /agent/exit`
- **THEN** the pending exit file SHALL remain and the node SHALL deliver it
