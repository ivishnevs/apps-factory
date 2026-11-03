Out of scope: messaging/inbox, decision records and approval grants are explicitly outside this change.

## ADDED Requirements

### Requirement: Native stdio MCP server
The native Codex runtime SHALL discover and invoke an actual stdio MCP server automatically. Task, project and agent metadata and lazy skills tools SHALL delegate authoritative operations to Core.

#### Scenario: Agent lists its tasks
- **WHEN** a Codex run calls the Factory MCP task list tool
- **THEN** the server SHALL answer from Core for the run's project

### Requirement: Explicit request keys and versions
Mutations SHALL require explicit stable request keys and versions where applicable.

#### Scenario: Mutation without a request key
- **WHEN** an agent calls a mutating tool without a request key
- **THEN** the call SHALL be refused

### Requirement: Lease-bound run credentials
Run credentials SHALL bind identity to an active lease and SHALL NOT access owner routes or another project. Calls and their outcomes SHALL persist across restart; terminal and expired tokens SHALL be rejected.

#### Scenario: Token of an ended run
- **WHEN** a call presents the run token of a terminal or expired run
- **THEN** Core SHALL reject it

#### Scenario: Owner route
- **WHEN** a run credential calls an owner route or another project's task
- **THEN** Core SHALL refuse it

### Requirement: Per-call policy and state checks
Current and captured policies, communication permissions, version and state checks SHALL apply per call. Submit SHALL NOT release a running project's writer; self-review SHALL be forbidden. Missing approval grants SHALL fail closed with 423; no implicit approval SHALL be granted.

#### Scenario: Self-review
- **WHEN** an agent tries to accept a task it implemented
- **THEN** Core SHALL refuse the review

#### Scenario: Missing grant
- **WHEN** a call requires an approval grant that does not exist
- **THEN** Core SHALL answer 423

### Requirement: Real smoke
A real smoke SHALL demonstrate manager-created and assigned tasks, executor MCP submit and manager acceptance.

#### Scenario: Manager plans and reviews
- **WHEN** the smoke runs a manager and an executor against Core
- **THEN** the manager SHALL create and assign a task, the executor SHALL submit it through MCP and the manager SHALL accept it

### Requirement: Argument errors list every problem
Core SHALL validate tool arguments against the tool's input schema and, on failure, answer 400 with one message naming all missing required arguments, all unknown arguments together with the accepted argument names, and all type errors, with nested paths, so a call with guessed arguments needs at most one corrected retry.

#### Scenario: Guessed arguments
- **WHEN** an agent calls `factory_artifact_request_approval` without `reason` and `request_id` and with an extra `summary`
- **THEN** Core SHALL answer 400 `missing required arguments: reason, request_id; unknown arguments: summary (accepted arguments: ...)` in one response
