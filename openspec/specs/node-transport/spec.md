# node-transport Specification

## Purpose
Let nodes reach Core over signed HTTPS alone: a long poll brings offers and stops, claims take runs, and run events
arrive in order from a spool on the node, so no broker can cross companies.

## Requirements

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

### Requirement: Session affinity
The first node that claims an agent's run on a task (or a conversation) SHALL hold that (agent, task) pair: later runs
of the pair SHALL be offered to and claimable by only that node while it is active, however long it is busy. When the
node becomes inactive (offline, paused by the owner or by Core for a critical disk, removed, or unable to run the agent) a release timer SHALL start; it SHALL be
cleared if the node is active again before it ends. When it ends with the node still inactive, the lock SHALL be
released: any capable node MAY take the run, start a fresh session and become the holder. A run without a session to
continue SHALL have no affinity. The pin of unpushed work SHALL keep its own rules.

#### Scenario: Rework goes back to its node
- **WHEN** node A ran the developer's task, QA returned it for rework and both nodes are idle
- **THEN** only node A SHALL get and claim the rework run, and it SHALL resume the session

#### Scenario: Busy holder
- **WHEN** node A holds the pair and has no free slot for an hour while node B is idle
- **THEN** the run SHALL wait for node A and the lock SHALL NOT be released

#### Scenario: Holder back in time
- **WHEN** node A goes offline and comes back before the release timeout
- **THEN** the timer SHALL be cleared and node A SHALL keep the lock

#### Scenario: Holder gone
- **WHEN** node A stays offline beyond the release timeout
- **THEN** another capable node SHALL take the run, start a fresh session with the context from Core and hold the lock
