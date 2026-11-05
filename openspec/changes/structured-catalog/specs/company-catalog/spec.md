## ADDED Requirements

### Requirement: Structured catalog storage
A company's catalog SHALL be stored as structured rows with stable ids and references between entities, so
changing one property of an entity SHALL change one row and its references SHALL follow without rewriting other
entities.

#### Scenario: Renaming a team
- **WHEN** an owner changes a team's display name
- **THEN** only the team's row SHALL change, and its agents, skill scopes and projects SHALL keep pointing at it

### Requirement: Display names
Teams and agents SHALL have a display name separate from their identifier, editable by owners and admins.

#### Scenario: Title in the UI
- **WHEN** an owner renames team `game` to "Gnome Forge" in the UI
- **THEN** the UI SHALL show "Gnome Forge" and the team's identifier SHALL stay unchanged
