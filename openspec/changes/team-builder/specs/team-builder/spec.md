## ADDED Requirements

### Requirement: Agent and team presets
Core SHALL offer agent presets (agent definition, prompt, Factory skills) and team presets (agent presets with reporting lines, profile, policies and company settings), versioned with the release. Presets SHALL name only built-in skills.

#### Scenario: Preset gallery
- **WHEN** an owner opens the presets
- **THEN** Core SHALL list each agent and team preset with title, description, role, skills and version

### Requirement: Presets copied into the catalog
Creating a company or an agent from a preset SHALL copy all its documents, prompts included, into a new catalog version of the company and install its Factory skills. The copied agent SHALL record its preset name and version. The company SHALL NOT depend on preset files afterwards.

#### Scenario: Company from a team preset
- **WHEN** a user creates a company from the `full-team` preset
- **THEN** version 1 of its catalog SHALL hold the team's agents, their prompts, profile, policies, company settings and installed skills

#### Scenario: Preset changes in a later release
- **WHEN** a release changes the `ios-developer` preset's prompt
- **THEN** existing copies SHALL keep their prompt and the UI SHALL show an update with a diff

### Requirement: Custom team and agents
An owner SHALL be able to start with an empty company and add agents from agent presets or fully custom, and change or remove them, through the API and the web UI. Every change SHALL be saved as a catalog version through the catalog's validation.

#### Scenario: Agent from a preset with changes
- **WHEN** an owner adds the `ios-developer` preset as `ios`, reporting to `product-owner`, with an extra installed skill
- **THEN** the catalog SHALL get an agent `ios` with the preset's prompt and skills plus the extra skill, reporting to `product-owner`

#### Scenario: Invalid custom agent
- **WHEN** an owner saves a custom agent that reports to an unknown agent
- **THEN** Core SHALL refuse the save with the error at the `reports_to` field and keep the current version

#### Scenario: Removing a lead
- **WHEN** an owner deletes an agent other agents report to
- **THEN** Core SHALL refuse the deletion naming those agents

### Requirement: Forms and documents agree
The structured agent API SHALL read and write the same catalog documents as the document editor, with optimistic concurrency on the catalog version.

#### Scenario: Concurrent edit
- **WHEN** an agent form saved on version 4 arrives while version 5 is current
- **THEN** Core SHALL refuse it as a conflict
