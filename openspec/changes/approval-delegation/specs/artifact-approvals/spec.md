## ADDED Requirements

### Requirement: Who approves is configurable
A team or project SHALL set who approves each kind of artifact: people only (a hard gate), a named agent, or the lead
deciding from the owner's earlier answers and recorded decisions. An agent approval SHALL record the agent, the setting
and what it relied on; anything not covered SHALL go to people. In the people-only mode no agent SHALL be able to
approve.

#### Scenario: Internal art delegated
- **WHEN** internal art of the game team is set to be approved by game-designer and artist-2d submits a concept pack
- **THEN** game-designer SHALL be able to approve it without the owner, and the approval SHALL name it and the setting

#### Scenario: Hard gate on store material
- **WHEN** external artifacts are set to people only and the lead tries to approve a store screenshot
- **THEN** Core SHALL refuse and the approval SHALL wait for a person
