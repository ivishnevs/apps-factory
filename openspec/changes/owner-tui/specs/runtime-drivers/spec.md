## ADDED Requirements

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
