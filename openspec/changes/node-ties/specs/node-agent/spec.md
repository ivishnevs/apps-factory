## ADDED Requirements

### Requirement: Node ties
Core SHALL describe every reason a task or a queued run may run only on one node as a tie with that node, a reason
(`work`: its unsaved work is only there; `session`: the agent's session is there), when it started and how it is
released, and the web, the TUI, escalations and docs SHALL use that wording. A work tie SHALL be released only when the
work is saved remotely or the owner resets the workspace; a session tie SHALL be released by the session-affinity
rules.

#### Scenario: Two reasons, one wording
- **WHEN** VP-3 waits for `hetzner-hel1` because its unsaved work is there and a conversation run waits for the Mac
  because its session is there
- **THEN** both SHALL show as "tied to <node>" with their reason and how the tie ends
