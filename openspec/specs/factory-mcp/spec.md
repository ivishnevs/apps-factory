# factory-mcp Specification

## Purpose
Expose Factory to agents through a native MCP server with lease-bound credentials and per-call checks.

## Requirements

### Requirement: Native stdio MCP server
The native Codex runtime SHALL discover and invoke an actual stdio MCP server automatically. Task, project and agent metadata tools SHALL delegate authoritative operations to Core. The server SHALL NOT offer skills tools to runs: a run's skills reach the runtime natively (`skill-registry`).

#### Scenario: Agent lists its tasks
- **WHEN** a Codex run calls the Factory MCP task list tool
- **THEN** the server SHALL answer from Core for the run's project

#### Scenario: No skills tools
- **WHEN** a run lists its Factory MCP tools
- **THEN** no factory_skills_list, factory_skills_search or factory_skills_read SHALL be among them

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

### Requirement: Brief required for agents reporting to a person
When a run's agent has a person (the owner or a member) as its manager, the tools that can bring an item to people SHALL list `brief` as a required field and their description SHALL start with what the brief must contain; for agents managed by an agent the schema SHALL keep `brief` optional.

#### Scenario: Lead forwards to the owner
- **WHEN** the tool list is built for an agent whose manager is the owner
- **THEN** `factory_question_forward` SHALL require `brief`

### Requirement: Closed sets are declared in the tool schemas
Every tool argument whose valid values are a closed set SHALL be declared with an `enum` built from the same constant Core validates against, the adapter SHALL check enums before the call leaves the node, and every error for a value outside a closed set SHALL list the allowed values. An unknown filter value SHALL be refused, not answered with an empty result.

#### Scenario: Unknown report kind
- **WHEN** an agent calls `factory_report_create` with `kind: audit`
- **THEN** the call SHALL fail with an error that lists every allowed report kind

#### Scenario: Schema and validator agree
- **WHEN** the tool schemas are built
- **THEN** each closed-set argument's enum SHALL equal the set its validator accepts

### Requirement: Core's own agent texts use only valid calls
Every agent-facing text Core generates (onboarding and audit briefs, review briefs, hold hints, reminders) SHALL name only tools that exist and argument values those tools accept, checked by a test.

#### Scenario: A brief names an invalid value
- **WHEN** a Core-generated brief names a report kind that Core does not accept
- **THEN** the test SHALL fail
