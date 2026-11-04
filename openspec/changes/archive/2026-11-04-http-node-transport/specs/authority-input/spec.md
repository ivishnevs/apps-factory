## ADDED Requirements

### Requirement: Urgent stop and resume through the node poll
An urgent directive or urgent artifact approval SHALL revoke the live run, wake the waiting poll of the run's node with a stop for that run, and rely on the heartbeat response as the guarantee within one heartbeat interval. The next run SHALL resume the same session with the directive first in its brief, and the revoked run SHALL NOT count as an attempt.

#### Scenario: Poll not waiting
- **WHEN** the run's node has no poll waiting when the stop is committed
- **THEN** the node SHALL get the stop in its next poll or heartbeat answer and the run outcome SHALL be `revoked`

#### Scenario: Stop while runs are queued
- **WHEN** an urgent directive is sent while runs of the company are queued
- **THEN** the stop SHALL reach the run's node without waiting behind those runs

## REMOVED Requirements

### Requirement: Urgent stop and resume
**Reason**: The stop no longer travels on a per-node NATS control subject (`http-node-transport`); replaced by "Urgent stop and resume through the node poll".
**Migration**: None (not released).
