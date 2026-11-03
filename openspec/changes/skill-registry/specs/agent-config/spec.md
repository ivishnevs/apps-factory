## MODIFIED Requirements

### Requirement: Skills
Only skill metadata SHALL be included at startup. Reads SHALL be scoped to the selected agent's explicit skills or enabled discovery categories and confined to the skill's own content. The skill registry SHALL be the company's installed skills in its catalog's `skills.yaml`, each naming its source and content hash; a company SHALL have the built-in Factory skills available to install and no installed skill it did not install.

#### Scenario: Resource outside the skill
- **WHEN** an agent reads a resource path that escapes the skill's directory
- **THEN** the read SHALL be refused

#### Scenario: Skill not installed
- **WHEN** an agent of a company includes a skill the company has not installed
- **THEN** loading SHALL fail with `unknown skill`
