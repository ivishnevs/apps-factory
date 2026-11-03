## MODIFIED Requirements

### Requirement: Existing projects are onboarded
A project created with repository mode `existing` (a workspace path on the node and/or a repository URL the node clones) SHALL be set up before any of its tasks runs: the node SHALL check the workspace or clone the repository and report it to Core. The repository's `openspec/` SHALL be left as it is (not imported, not untracked). When the owner asks for an audit, Core SHALL create a ready audit task for the audit assignee of the project's team.

#### Scenario: Existing repository tracks openspec
- **WHEN** the owner adds an existing repository whose git tracks `openspec/`
- **THEN** the first task's run SHALL see the repository's OpenSpec tree in its checkout and the repository SHALL still track it

#### Scenario: Audit first
- **WHEN** the owner adds an existing project of team `saas` with "Run an audit first"
- **THEN** Core SHALL create one ready audit task assigned to the `saas` team's audit assignee, and its run SHALL commit the baseline specs to the repository with its pull request
