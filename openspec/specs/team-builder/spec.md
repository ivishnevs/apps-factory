# team-builder Specification

## Purpose
Let a company build its teams from the release's presets, copied into its catalog as ordinary documents, edit
agents as fields, and see what keeps its work from running.

## Requirements

### Requirement: Agent and team presets
Core SHALL offer agent presets (agent definition, prompt, Factory skills, secrets it needs) and team presets (team document, agent presets with reporting lines and lead, optional profile and policies), versioned with the release. Presets SHALL name only built-in skills.

#### Scenario: Preset gallery
- **WHEN** an owner opens the presets
- **THEN** Core SHALL list each agent and team preset with title, description, role, skills, needed secrets and version

### Requirement: Presets copied into the catalog
Creating a team or an agent from a preset SHALL copy all its documents, prompts included, into a new catalog version of the company and install its Factory skills. Copied agents SHALL record their preset name and version. The company SHALL NOT depend on preset files afterwards.

#### Scenario: Team from a preset in an existing company
- **WHEN** an owner whose company has team `saas` creates team `gamedev` from the `game-dev` preset
- **THEN** the new catalog version SHALL hold team `gamedev` with its lead, agents, prompts and installed skills, and team `saas` SHALL be unchanged

#### Scenario: Preset changes in a later release
- **WHEN** a release changes the `ios-developer` preset's prompt
- **THEN** existing copies SHALL keep their prompt and the UI SHALL show an update with a diff

### Requirement: Custom teams and agents
An owner or admin SHALL be able to create empty teams and add agents to a team from agent presets or fully custom, and change or remove them, through the API and the web UI. Every change SHALL be saved as a catalog version through the catalog's validation.

#### Scenario: Agent from a preset with changes
- **WHEN** an owner adds the `ios-developer` preset to team `gamedev` as `gamedev-ios`, reporting to the team lead, with an extra installed skill
- **THEN** the catalog SHALL get agent `gamedev-ios` in team `gamedev` with the preset's prompt and skills plus the extra skill

#### Scenario: Invalid custom agent
- **WHEN** an owner saves a custom agent that reports to an unknown agent
- **THEN** Core SHALL refuse the save with the error at the `reports_to` field and keep the current version

#### Scenario: Removing a lead
- **WHEN** an owner deletes an agent other agents report to or that leads its team
- **THEN** Core SHALL refuse the deletion naming the reason

### Requirement: Setup status without blocking
Core SHALL report what keeps a company's agents from running (secrets they need that are not set, no enrolled node) and SHALL save teams and agents regardless.

#### Scenario: Preset needs a secret
- **WHEN** an owner creates a team whose researcher needs `APPSTORETRACKER_KEY` and the company has not set it
- **THEN** the team SHALL be saved, the setup status SHALL name the secret and the agent, and the researcher's tasks SHALL wait until it is set

### Requirement: Forms and documents agree
The structured team and agent API SHALL read and write the same catalog documents as the document editor, with optimistic concurrency on the catalog version.

#### Scenario: Concurrent edit
- **WHEN** an agent form saved on version 4 arrives while version 5 is current
- **THEN** Core SHALL refuse it as a conflict

### Requirement: Team builder in the web UI
The web UI SHALL let owners and admins set a company up and change its teams with forms over the team builder API: a setup wizard, a new team from a preset, an agent from a preset or custom, an agent form with field errors, and team pages with agent cards, preset update hints and the setup status.

#### Scenario: First setup
- **WHEN** an owner opens the setup wizard of a new company
- **THEN** the wizard SHALL walk through the team, its agents, the missing secrets, a node and the first project, showing what is still missing

#### Scenario: Field error
- **WHEN** an owner saves an agent whose manager does not exist
- **THEN** the form SHALL show the error at the "reports to" field and save nothing
