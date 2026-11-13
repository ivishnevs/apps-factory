## ADDED Requirements

### Requirement: Ended runs release lost runs
When a node's heartbeat lists a run as ended and Core holds that run lost and unreleased, Core SHALL release the run (its task retries after the usual backoff unless the run has an accepted exit) and ack it so the node forgets it, whether or not its terminal event was applied. Late terminal evidence SHALL still replace the lost outcome.

#### Scenario: Terminal event never arrives
- **WHEN** Core marked a run lost while its node was unreachable, the node ended the run, and its terminal event was lost
- **THEN** the next heartbeat listing the run as ended SHALL release it, the node SHALL drop it on the ack, and the task SHALL run again

## MODIFIED Requirements

### Requirement: Sleep-safe liveness
Core SHALL NOT expire a lease or start window until it has been ticking continuously for longer than the TTL since the later of the last renewal and the start of its current continuity. Worker timeouts SHALL be measured with a clock that does not advance during system sleep. A heartbeat refused only because its signature fell outside the time window SHALL keep the node online for at most one more TTL.

#### Scenario: Laptop sleeps for two hours
- **WHEN** the machine running Core and the worker sleeps for two hours and wakes up
- **THEN** no run SHALL be marked lost or timed out because of the sleep, provided its worker heartbeats within one TTL after wake

#### Scenario: Clocks apart right after wake
- **WHEN** after a wake Core refuses the node's heartbeats for the time window for 30 s and then accepts a corrected one
- **THEN** the node SHALL stay online and none of its runs SHALL be marked lost
