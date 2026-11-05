## ADDED Requirements

### Requirement: Default library across fields
Every company SHALL see a built-in library of at least 30 agent presets from different fields (software, games,
growth, content, business), each with a category, a plain description, example tasks, its requirements and its
skills, and team presets per field.

#### Scenario: Non-technical user picks an agent
- **WHEN** a new user opens the library and filters by "Growth"
- **THEN** marketing, SEO, content and similar presets SHALL be listed with what they do and what they need

### Requirement: Presets come with their skills
Each preset SHALL name only skills from the built-in Factory source, shipped in the release and pinned by hash, so
adding a preset SHALL need no skill source.

#### Scenario: Adding a preset
- **WHEN** a user adds the "SEO specialist" preset to a team
- **THEN** the agent SHALL get its skills installed from the Factory source without the user adding a source

### Requirement: Custom sources are the advanced path
Git, private and archive skill sources SHALL remain available to owners and admins as an advanced option and SHALL
NOT be needed by any default flow.

#### Scenario: Default onboarding
- **WHEN** a user creates a team from a preset
- **THEN** the flow SHALL not ask for any repository or archive

### Requirement: Vendored skills are licensed
A skill vendored from another source SHALL have a permissive license recorded with attribution in the release.

#### Scenario: Skill without a license
- **WHEN** a candidate skill has no license or a non-permissive one
- **THEN** it SHALL not ship in the Factory source
