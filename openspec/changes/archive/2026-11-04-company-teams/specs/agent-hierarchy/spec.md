## MODIFIED Requirements

### Requirement: Reporting tree as data
Each agent SHALL declare `reports_to` as another agent of the same team or `owner`, and the agents reporting to `owner` SHALL be exactly the team leads, one per team. The tree MAY have any depth, role names SHALL be data, and configuration with cycles, unknown targets or a manager in another team SHALL be rejected.

#### Scenario: Cycle
- **WHEN** agent A reports to B and B reports to A
- **THEN** configuration validation SHALL fail and the catalog SHALL NOT be loaded

#### Scenario: Manager in another team
- **WHEN** an agent of team `gamedev` reports to an agent of team `saas`
- **THEN** configuration validation SHALL fail naming both agents
