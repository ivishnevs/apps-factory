## ADDED Requirements

### Requirement: Clock skew correction
A refusal for the signature time window SHALL carry Core's time and a machine-readable reason. The node SHALL keep an offset to Core's clock learned from such answers, bounded to one hour, sign later requests with the corrected time, retry the refused request at once, and report the offset in its heartbeat. Core SHALL emit `node.clock_skew` once per episode when the offset exceeds the window. Nonce replay protection and the 120 s window around the signed time SHALL stay unchanged.

#### Scenario: Core clock behind after wake
- **WHEN** Core's clock is 5 minutes behind the node's after the host wakes
- **THEN** the node's first request SHALL be refused with Core's time, the retry signed with the corrected time SHALL be accepted, and the owner SHALL see one `node.clock_skew` event
