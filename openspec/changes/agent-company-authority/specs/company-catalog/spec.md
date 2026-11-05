## ADDED Requirements

### Requirement: Agents' catalog authority is scoped and visible
An agent's catalog changes SHALL be limited to the scope it was granted (by default its own team), changes outside
it SHALL be refused, and each agent's authority SHALL be shown to owners in plain words where they manage the team.

#### Scenario: Lead tries to delete another team
- **WHEN** a team lead without company scope proposes a catalog change deleting another team
- **THEN** Core SHALL refuse it, saying that the lead may change only its own team

#### Scenario: Owner reviews a catalog change
- **WHEN** an owner opens an approval of an agent's catalog change
- **THEN** the approval SHALL list in plain words what is added, changed and deleted and which teams are affected,
  and destructive changes SHALL be highlighted
