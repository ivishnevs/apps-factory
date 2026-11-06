## ADDED Requirements

### Requirement: Context window per agent
An agent or profile MAY set `spec.runtime.context_window` in tokens; the catalog SHALL validate it against the
provider's limits and pass it to every run of the agent. Unset SHALL mean the model's full window.

#### Scenario: Conversation agent with 200k
- **WHEN** the producer has `context_window: 200k` and its conversation session passes 200k tokens
- **THEN** the runtime SHALL compact the session instead of growing towards 1M

#### Scenario: Invalid value
- **WHEN** an agent sets a window larger than its model allows
- **THEN** the catalog SHALL refuse the change naming the agent and the limit
