## ADDED Requirements

### Requirement: Nodes use only signed HTTPS
A node SHALL exchange everything with Core through signed node requests over HTTPS. Core SHALL run no message broker for nodes, and every node route SHALL refuse runs, tasks and jobs of another company or another node.

#### Scenario: Run of another company
- **WHEN** a node of company A sends events or a claim for a run of company B
- **THEN** Core SHALL answer not found and change nothing

#### Scenario: Revoked node
- **WHEN** an owner revokes a node that has a poll waiting
- **THEN** the poll SHALL end and every later request of that node SHALL get 401

### Requirement: Long poll for node work
Core SHALL serve `POST /nodes/{node_id}/poll`, waiting at most 25 seconds, and SHALL answer as soon as the node has offers, stops, resets, cleanups, a hooks change or sync jobs. A new poll of the same node SHALL end the older one with an empty answer.

#### Scenario: Work arrives while the node waits
- **WHEN** a run the node can take is queued while its poll waits
- **THEN** the poll SHALL answer with that run's offer without waiting for the end of the wait

#### Scenario: Nothing to do
- **WHEN** nothing arrives for the node during the wait
- **THEN** the poll SHALL answer empty after the wait

### Requirement: Offers and claim
Offers SHALL list queued runs of the node's company that the node can take (free slots for the provider, resource class, labels, project setup), oldest first, at most its free slots. Only the signed claim SHALL take a run, atomically; a node that loses a claim race SHALL get a conflict.

#### Scenario: Two nodes, one run
- **WHEN** two nodes of a company receive the same offer and both claim it
- **THEN** exactly one claim SHALL succeed and the other SHALL get 409

#### Scenario: Paused node
- **WHEN** a node is paused or has no usable driver
- **THEN** its polls SHALL get no offers

### Requirement: Prompt stops without a broker
An urgent stop SHALL wake the waiting poll of the run's node and reach it in the poll answer; the heartbeat answer SHALL remain the guarantee within one heartbeat interval.

#### Scenario: Stop while the node waits
- **WHEN** the owner sends an urgent directive while the run's node has a poll waiting
- **THEN** the poll SHALL answer with the stop immediately and the node SHALL stop the run

### Requirement: Run events over HTTP
A node SHALL send a run's events to `POST /runs/{run}/events` with the run's lease in batches ordered by a per-run sequence. Core SHALL apply them in order, skip events already applied, refuse a gap naming the expected sequence, and answer the highest applied sequence.

#### Scenario: Batch sent twice
- **WHEN** a node sends the same batch again after a timeout
- **THEN** Core SHALL apply each event once and answer the same applied sequence

#### Scenario: Gap
- **WHEN** a batch starts after a sequence Core has not applied
- **THEN** Core SHALL refuse it with the expected sequence and the node SHALL resend from there

### Requirement: Node event spool
A node SHALL write every run event to a spool in its state directory before sending it, send unapplied events in order when Core is reachable, resume spools after a restart, and delete a run's spool only after its terminal event is applied.

#### Scenario: Core restarts during a run
- **WHEN** Core is unreachable for a minute while a run emits events and ends
- **THEN** the run SHALL continue, and once Core is back every event SHALL be applied in order and the run SHALL end with its real outcome
