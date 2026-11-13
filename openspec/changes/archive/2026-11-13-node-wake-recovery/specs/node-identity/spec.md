## ADDED Requirements

### Requirement: Clock skew correction
A refusal for the signature time window SHALL carry Core's time (in the answer and its `Date` header) and a machine-readable reason. The node SHALL keep an offset to Core's clock learned from such answers, bounded to one hour, sign later requests with the corrected time, retry the refused request at once, and report the offset in its heartbeat. Nonce replay protection and the 120 s window around the signed time SHALL stay unchanged.

#### Scenario: Core clock behind after wake
- **WHEN** Core's clock is 5 minutes behind the node's after the host wakes
- **THEN** the node's first request SHALL be refused with Core's time, the retry signed with the corrected time SHALL be accepted, and the owner SHALL see one `node.clock_skew` event

#### Scenario: Clocks more than an hour apart
- **WHEN** Core's answer shows its clock two hours behind the node's
- **THEN** the node SHALL keep signing with its previous offset, SHALL NOT retry, and SHALL report the observed offset

### Requirement: Clock skew report
Core SHALL emit `node.clock_skew` once per episode when a node's reported offset exceeds the signature window and re-arm it when the offset is back within the window. The Nodes page SHALL show the offset.

#### Scenario: One event per episode
- **WHEN** a node reports an offset of 5 minutes in three heartbeats, then none, then 200 s
- **THEN** the owner SHALL see `node.clock_skew` twice, once per episode
