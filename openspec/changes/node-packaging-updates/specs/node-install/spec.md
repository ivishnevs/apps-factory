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

### Requirement: Nodes report their version and Core enforces a minimum
A node SHALL report its package version when it registers; the Nodes page SHALL show it; a node below Core's
`min_node_version` SHALL get no work and its card SHALL say "update required".

#### Scenario: Old node
- **WHEN** a node of version 2026.11.01 registers with a Core whose minimum is 2026.11.12
- **THEN** Core SHALL offer it no run, refuse its claims with `update_required` and show "update required"

### Requirement: Node settings from the web
Owners and admins SHALL be able to change a node's slots, conversation slots, labels, drivers enabled and disk
watermarks from the web; the node SHALL apply them without a restart over its node.yaml, refuse what node.yaml pins
or caps, and the web SHALL show each setting as applied, pending or refused next to node.yaml's value. Trust and
machine settings SHALL stay in node.yaml only.

#### Scenario: More slots
- **WHEN** the owner sets 3 slots on a node whose node.yaml says 1 and `slots_max: 2`
- **THEN** the node SHALL run 2 slots without a restart and the card SHALL show slots applied, capped at slots_max 2

### Requirement: Detected node labels
A node SHALL detect and report what it can do (OS, architecture, Xcode, iOS simulator, headless, GPU) next to the
labels its owner sets; runs SHALL select on both.

#### Scenario: Mac with Xcode
- **WHEN** an agent needs `labels: [macos, xcode]` and a Mac with Xcode has no labels in node.yaml
- **THEN** Core SHALL let that Mac claim the agent's runs
