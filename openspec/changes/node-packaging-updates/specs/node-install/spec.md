## ADDED Requirements

### Requirement: Node without runtime prerequisites
A node package SHALL run its drivers without the person installing Node.js or other language runtimes; only the agent
runtimes themselves (Claude Code, Codex) and git remain prerequisites.

#### Scenario: Fresh Mac
- **WHEN** a person without Node.js installs a node
- **THEN** the Claude driver SHALL work and doctor SHALL not ask for Node.js

### Requirement: Nodes update themselves safely
A node SHALL find a newer signed release on Core, verify it, switch only when it has no live runs, and roll back to
the previous version if the new one does not start healthy; the company SHALL choose automatic updates or updates
with approval.

#### Scenario: Update with runs in progress
- **WHEN** a new release appears while the node runs two tasks
- **THEN** the node SHALL stop claiming, wait for both to end, switch, and resume claiming after a health check
