## ADDED Requirements

### Requirement: Drivers apply the context window
The Claude and Codex drivers SHALL apply a run's `context_window` as the runtime's auto-compact limit for that run's
process only, so parallel runs of one agent each keep their own context under the same limit, and a resumed session
SHALL be compacted once it passes the limit.

#### Scenario: Two parallel runs
- **WHEN** gameplay-dev with `context_window: 400k` runs two tasks at once
- **THEN** each run SHALL compact its own session at 400k, independently of the other
