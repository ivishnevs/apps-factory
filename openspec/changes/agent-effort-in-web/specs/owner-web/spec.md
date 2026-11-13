## ADDED Requirements

### Requirement: Agent effort in the web
The web SHALL show and edit an agent's reasoning effort: the agent form SHALL offer the efforts its runtime accepts
(empty keeps the runtime's default) and save them to the catalog with history; the team member card SHALL show the
agent's model and effort; the run page SHALL show the effort the run used next to its model.

#### Scenario: Raise an agent's effort
- **WHEN** a person sets an agent's effort from low to medium in the agent form
- **THEN** the catalog SHALL record the change and the agent's next runs SHALL use medium

#### Scenario: Run page
- **WHEN** a person opens a run of an agent with effort high
- **THEN** the run page SHALL show the model and "high"
