## ADDED Requirements

### Requirement: Optional conversation model
An agent or its profile MAY set `spec.runtime.conversation_model`, a model of the agent's provider. Conversation runs
SHALL use it as their model and work runs SHALL keep `spec.runtime.model`. When it is unset, conversation runs SHALL
use `spec.runtime.model`, so existing catalogs keep their behaviour. A model that does not fit the provider SHALL be
refused when the catalog loads, naming the field. A task's model override SHALL still apply to its own task.

#### Scenario: Conversations on a lighter model
- **WHEN** the producer has `model: default` and `conversation_model: sonnet`
- **THEN** its conversation runs SHALL carry `model: sonnet` and its work runs `model: default`

#### Scenario: Unset keeps the agent's model
- **WHEN** an agent sets no `conversation_model`
- **THEN** its conversation runs SHALL carry its `model`, and its run snapshot SHALL not contain the field

#### Scenario: Another provider's model
- **WHEN** a Codex agent sets `conversation_model: sonnet`
- **THEN** the catalog SHALL refuse it as not a codex model
