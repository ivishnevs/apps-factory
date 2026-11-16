## ADDED Requirements

### Requirement: Node tools have minimum versions
Every tool the node itself runs SHALL have one entry in the node's tool table with its feature, platforms, minimum
version (or exact pin for a runtime), the reason for the minimum and the fix. The node SHALL check each tool's version
with the same parser and comparison as project requirements, in doctor, in its periodic self-checks and before taking
work, and SHALL treat a missing tool, a version below the minimum and a tool that reports no version alike.

#### Scenario: A tool too old
- **WHEN** gitleaks on the node reports 8.18.2 and the minimum is 8.21.0
- **THEN** `worktrees` SHALL fail with the version, the minimum and the fix, and the node SHALL take no runs of
  projects with a repository until a check passes

#### Scenario: A distribution build without a version
- **WHEN** gitleaks reports "version is set by build process"
- **THEN** the check SHALL fail as "reports no version" with the same fix

#### Scenario: Other work goes on
- **WHEN** only `worktrees` fails on a node that also serves agents without a repository
- **THEN** those runs SHALL still be offered to the node

### Requirement: Node tool versions are reported
The node SHALL report each tool's name, version, minimum and status in register and health; the Nodes page,
`factory nodes list` and `factory_nodes` SHALL show them with a failing tool's fix.

#### Scenario: Which node has the old git
- **WHEN** the owner opens the Nodes page
- **THEN** each node card SHALL list its tool versions and mark the ones below their minimum

### Requirement: Core raises node tool minimums
Core MAY send higher minimums for node tools in the heartbeat answer; the node SHALL apply the higher of its own and
Core's minimum and SHALL re-check at once. Core SHALL NOT lower a node's minimum.

#### Scenario: A broken version found in the field
- **WHEN** Core raises the git minimum to 2.40.0 while node N has git 2.39.5
- **THEN** N SHALL report the fault within one heartbeat and take no work that needs `worktrees`
