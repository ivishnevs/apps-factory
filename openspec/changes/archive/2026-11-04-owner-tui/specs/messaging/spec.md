## ADDED Requirements

### Requirement: Message feed rows name the route
A feed row written for a message SHALL carry the summary `sender → recipient (type): body`, so feed consumers (the owner UI's flow and activity, the CLI's `watch`, `log` and `show`) know both parties without reading the message.

#### Scenario: Question to a manager
- **WHEN** developer-1 sends a question to pm
- **THEN** the message's feed row summary SHALL read `developer-1 → pm (question): …` with the body
