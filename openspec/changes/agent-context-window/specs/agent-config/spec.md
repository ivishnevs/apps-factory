## ADDED Requirements

### Requirement: Context window per agent
Every agent SHALL resolve `spec.runtime.context_window` (tokens, or `max` for the model's maximum) from itself or its
profile; the catalog SHALL refuse an agent without it, validate it against the provider's limits and pass it to every
run of the agent. Presets, the team wizard and the editor SHALL write the maximum explicitly; there SHALL be no
implicit default.

#### Scenario: Conversation agent with 200k
- **WHEN** the producer has `context_window: 200k` and its conversation session passes 200k tokens
- **THEN** the runtime SHALL compact the session instead of growing towards 1M

#### Scenario: Invalid value
- **WHEN** an agent sets a window larger than its model allows
- **THEN** the catalog SHALL refuse the change naming the agent and the limit

#### Scenario: Missing window
- **WHEN** an owner saves an agent whose profile and agent both lack `context_window`
- **THEN** the catalog SHALL refuse the change naming the agent and the field
