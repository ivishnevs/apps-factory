## ADDED Requirements

### Requirement: Run event stream
Core SHALL serve a run's events as server-sent events at `GET /runs/{id}/events/stream` for the owner, with the event sequence as `id`, resuming after `Last-Event-ID` or `?after=`, starting at the last N events with `?tail=N`, and leaving out the worker lease. After an ended run's last event the stream SHALL send `event: end` with the run's status and outcome and close. The stream SHALL share its delivery loop with `GET /feed/stream`.

#### Scenario: Reconnect with Last-Event-ID
- **WHEN** a client reconnects with `Last-Event-ID` set to the last sequence it received
- **THEN** it SHALL receive every later event of the run exactly once

#### Scenario: Tail of a live run
- **WHEN** a client opens the stream of a live run with `?tail=20`
- **THEN** it SHALL first receive the run's last 20 events and then new events as they are stored

#### Scenario: Ended run
- **WHEN** a client streams a run that has ended
- **THEN** after the last event it SHALL receive `event: end` with `{status, outcome}` and the stream SHALL close

#### Scenario: Lease left out
- **WHEN** the run's stored events include the worker lease
- **THEN** the stream SHALL NOT contain it
