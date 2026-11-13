## MODIFIED Requirements

### Requirement: Node event spool
A node SHALL write every run event to a spool in its state directory before sending it, send unapplied events in order when Core is reachable, resume spools after a restart, and delete a run's spool only after its terminal event is applied or Core refuses the events with a final refusal (Node delivery refusals). A retry-class refusal SHALL be retried with backoff up to the retry horizon; past it the node SHALL raise a node health event naming the run and SHALL NOT drop the spool silently.

#### Scenario: Core restarts during a run
- **WHEN** Core is unreachable for a minute while a run emits events and ends
- **THEN** the run SHALL continue, and once Core is back every event SHALL be applied in order and the run SHALL end with its real outcome

#### Scenario: Signature refused after wake
- **WHEN** Core answers 401 `signature_window` to a run's terminal events right after the host wakes
- **THEN** the node SHALL keep the spool and resend, and the terminal event SHALL be applied once a request is accepted

## ADDED Requirements

### Requirement: Refusal reasons
Core SHALL name a machine-readable reason in every 401, 403 and 410 answer on node routes and run-credential routes, such as `signature_window`, `lease_changed`, `node_key_revoked`, `agent_unavailable` and `not_current_run`.

#### Scenario: Window refusal names itself
- **WHEN** Core refuses a node request because its signature time is outside the window
- **THEN** the answer SHALL carry the reason `signature_window`

### Requirement: Node delivery refusals
The node SHALL classify every refusal of run events and exits with one rule by reason and status. It SHALL retry network errors, 5xx, 408, 429 and temporary auth refusals. It SHALL treat as final another node's or an unknown run, 400, 422, 409, 410 and permanent auth refusals. Redelivery SHALL be idempotent.

#### Scenario: Lease changed
- **WHEN** a run's exit is refused with 401 `lease_changed` because the task was claimed again
- **THEN** the node SHALL record the exit as refused and SHALL NOT retry it

#### Scenario: Clock window
- **WHEN** a run's events are refused with 401 `signature_window`
- **THEN** the node SHALL keep and resend them
