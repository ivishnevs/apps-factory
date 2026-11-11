## ADDED Requirements

### Requirement: Owner MCP server
Factory SHALL offer an MCP server for the owner's own coding agent that reads the company's state through the owner API (status, pauses, nodes, why a task waits, runs with their errors and usage explained, catalog validation, an agent's effective skills and prompt) and SHALL perform a change only after showing its exact effect and getting the owner's confirmation. No tool SHALL output a secret value.

#### Scenario: Why the CEO does not answer
- **WHEN** the owner's agent asks why the CEO's chat in tracely has no answer
- **THEN** the server SHALL return the task's waiting reason in words with its fix, such as a paused project and how to resume it

#### Scenario: Change needs confirmation
- **WHEN** the owner's agent calls the tool that resumes a project
- **THEN** the server SHALL describe what will start running and SHALL resume only after the owner confirms

### Requirement: Setup and support runbooks
Factory SHALL ship a built-in `factory-setup` skill whose runbooks cover first setup, moving in from Paperclip, adding a machine, node logins, disk, DNS and proxy faults, an agent that does not answer, pauses and token usage. Each runbook SHALL end with a check that proves the fix.

#### Scenario: Adding a second Mac
- **WHEN** the owner asks their agent to add a second Mac that is reachable over Tailscale
- **THEN** the runbook SHALL lead through `tailscale serve` with https, enrolling and approving the node, and its own Claude login, and SHALL finish with a node `doctor` pass and a run that starts
