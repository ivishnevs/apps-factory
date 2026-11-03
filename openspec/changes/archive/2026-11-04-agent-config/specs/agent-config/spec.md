## ADDED Requirements

### Requirement: Configuration
Profiles SHALL merge with explicit child scalar precedence; inherited skills and policies SHALL be additive. Cycles, missing references, unknown fields and unsupported limits SHALL fail loading.

#### Scenario: Child overrides a scalar with false
- **WHEN** a profile sets a flag to true and the agent inheriting it sets the flag to false
- **THEN** the resolved agent SHALL have false and the profile's skills and policies SHALL be added to the agent's own

#### Scenario: Profile cycle
- **WHEN** two profiles inherit from each other
- **THEN** loading SHALL fail

### Requirement: Skills
Only skill metadata SHALL be included at startup. Reads SHALL be scoped to the selected agent's explicit skills or enabled discovery categories and confined to the configured skill directory. The skill registry SHALL be the `skills.yaml` of the company's catalog directory (`configs/skills.yaml` for `default`, `configs/<company>/skills.yaml` for another), with SKILL.md paths absolute or relative to that directory; a company without its own `skills.yaml` SHALL have no skills.

#### Scenario: Resource outside the skill
- **WHEN** an agent reads a resource path that escapes the skill's directory, including through a symlink
- **THEN** the read SHALL be refused

#### Scenario: Company without a registry
- **WHEN** an agent of a company without its own `skills.yaml` includes a skill
- **THEN** loading SHALL fail with `unknown skill`

### Requirement: Policies
One catalog SHALL contain access, escalation and reporting rules. Mandatory rules SHALL survive inheritance and unrestricted/never agent settings. Deny SHALL take precedence over approval/allow. Core skill operations SHALL check resolved access decisions before reading resource content.

#### Scenario: Unrestricted agent and a mandatory rule
- **WHEN** an agent is configured unrestricted and a mandatory policy denies an operation
- **THEN** the operation SHALL be denied

#### Scenario: Skill read denied
- **WHEN** the resolved access decision for a skill operation is deny
- **THEN** Core SHALL refuse it without reading the resource content
