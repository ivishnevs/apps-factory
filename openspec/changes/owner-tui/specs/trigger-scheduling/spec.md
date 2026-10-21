## ADDED Requirements

### Requirement: Why a run is queued
Core SHALL say why a QUEUED run is not claimed yet, as `queued` (`reason`, `detail`, `until`, and each registered node's `{node, name, reason, detail, until}`) on the run in `GET /status`, on `GET /runs/{id}` and in its task's `GET /tasks/{id}/why`, judging the claim's own checks without a claim. A run that is not QUEUED SHALL have no `queued`.

#### Scenario: Driver disabled by an auth failure
- **WHEN** the only node's driver for the run's provider was disabled by a run's authentication failure until T
- **THEN** the QUEUED run's `queued` SHALL be `driver_disabled` with the failing check and its detail and `until` T

#### Scenario: Claimed run
- **WHEN** the run was claimed by a node
- **THEN** it SHALL have no `queued`

### Requirement: Queued reason order
A pause (`paused`, with its reason and end), the agent's cap (`agent_busy`) and the pinned node (`pinned`) SHALL come before the nodes. Each node SHALL answer `node_offline`, `node_paused`, `driver_disabled`, `labels`, `no_slot` or `resource_busy`, and the node closest to taking the run SHALL give the reason; a node with room makes it `unclaimed`, no registered node `no_node`.

#### Scenario: Pause with a reason
- **WHEN** the owner paused the run's agent with a reason
- **THEN** `queued` SHALL be `paused` with that reason, before any node's answer

#### Scenario: Several nodes
- **WHEN** one node has every slot held and another is offline
- **THEN** `queued` SHALL be `no_slot` from the full node and list both nodes' answers

#### Scenario: Room on a node
- **WHEN** a node has the driver, labels, a slot and the resources for the run
- **THEN** `queued` SHALL be `unclaimed`

#### Scenario: Critical disk and owner pause
- **WHEN** the node reports its disk below the critical watermark, or the owner paused it with a reason
- **THEN** the node's answer SHALL be `node_paused` with the free space and the watermark, or with the owner's reason
