## ADDED Requirements

### Requirement: Brief required for agents reporting to a person
When a run's agent has a person (the owner or a member) as its manager, the tools that can bring an item to people SHALL list `brief` as a required field and their description SHALL start with what the brief must contain; for agents managed by an agent the schema SHALL keep `brief` optional.

#### Scenario: Lead forwards to the owner
- **WHEN** the tool list is built for an agent whose manager is the owner
- **THEN** `factory_question_forward` SHALL require `brief`
