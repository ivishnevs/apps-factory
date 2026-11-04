## ADDED Requirements

### Requirement: Team builder in the web UI
The web UI SHALL let owners and admins set a company up and change its teams with forms over the team builder API: a setup wizard, a new team from a preset, an agent from a preset or custom, an agent form with field errors, and team pages with agent cards, preset update hints and the setup status.

#### Scenario: First setup
- **WHEN** an owner opens the setup wizard of a new company
- **THEN** the wizard SHALL walk through the team, its agents, the missing secrets, a node and the first project, showing what is still missing

#### Scenario: Field error
- **WHEN** an owner saves an agent whose manager does not exist
- **THEN** the form SHALL show the error at the "reports to" field and save nothing
