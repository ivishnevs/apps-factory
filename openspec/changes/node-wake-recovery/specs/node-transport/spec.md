## MODIFIED Requirements

### Requirement: Node event spool
A node SHALL write every run event to a spool in its state directory before sending it, send unapplied events in order when Core is reachable, resume spools after a restart, and delete a run's spool only after its terminal event is applied or Core refuses the events in a way that can never apply (another node's or an unknown run, invalid events). An authentication or authorization refusal (401, 403) SHALL be retried with backoff and SHALL NOT delete the spool; a spool kept past the retry horizon SHALL raise a node health event.

#### Scenario: Core restarts during a run
- **WHEN** Core is unreachable for a minute while a run emits events and ends
- **THEN** the run SHALL continue, and once Core is back every event SHALL be applied in order and the run SHALL end with its real outcome

#### Scenario: Signature refused after wake
- **WHEN** Core answers 401 to a run's terminal events because the request fell outside the signature window
- **THEN** the node SHALL keep the spool and resend, and the terminal event SHALL be applied once a request is accepted
