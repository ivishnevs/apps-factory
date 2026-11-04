# external-mcp-servers Specification

## Purpose
Let a company declare external MCP servers, validate them and give them to the agents that opt in.

## Requirements

### Requirement: Company MCP servers and agent opt-in
A company catalog SHALL declare external MCP servers in `mcp_servers.yaml` of its catalog directory, each with a
name, `transport: http`, a `url`, optional `headers` and optional `deny_tools`. An agent SHALL get only the
servers its `spec.mcp` names, merged as a set from its profiles, and its config snapshot SHALL carry their
definitions with header secrets as unresolved `${secret:NAME}` references.

#### Scenario: Agent opts in
- **WHEN** `mcp_servers.yaml` declares `appstoretracker` and an agent sets `mcp: [appstoretracker]`
- **THEN** the agent's config snapshot SHALL carry that server's definition with its `${secret:NAME}` references unresolved and no other server

### Requirement: MCP server validation
Loading SHALL fail on an unknown server name, a URL that is not absolute `https` (plain `http` only to a loopback
host), a transport other than `http`, the reserved name `factory`, an invalid tool name, a `${...}` other than
`${secret:NAME}`, and a credential-looking header (authorization, token, key, secret, cookie, password,
credential, session) whose value holds no `${secret:NAME}` reference.

#### Scenario: Literal credential
- **WHEN** a server's `Authorization` header is `Bearer abc123`
- **THEN** loading SHALL fail saying the value must reference a company secret

#### Scenario: Unknown server
- **WHEN** an agent sets `mcp: [nope]` and no server `nope` is declared
- **THEN** loading SHALL fail naming `nope`

### Requirement: Claude agents only
External MCP servers SHALL be supported for claude agents only: loading SHALL fail on a codex agent with
`spec.mcp`, and the codex driver SHALL refuse a launch with external servers.

#### Scenario: Codex agent
- **WHEN** a codex agent sets `spec.mcp`
- **THEN** loading SHALL fail

### Requirement: Missing secret fails setup
A run whose servers need a secret that is missing in Core at claim (deleted after dispatch; before dispatch the task
waits, `core-secret-store`), or whose sealed value the node cannot open, SHALL end `setup_failed` before the runtime starts, with a reason naming the server and the secret and never a
value.

#### Scenario: Missing secret
- **WHEN** a run's server references `${secret:ASTR_API_KEY}` and the secret is deleted after the run was dispatched
- **THEN** the run SHALL end `setup_failed` with a reason naming `ASTR_API_KEY` and no runtime SHALL be launched

### Requirement: Claude runtime servers
The Claude driver SHALL add each external server to the runtime's MCP configuration as type `http` next to
`factory`, keep strict MCP config, and disallow `mcp__<server>__<tool>` for each denied tool. The secret SHALL
reach Claude Code only through its environment (`FACTORY_MCP_SECRET_<NAME>`, referenced from the header as
`${FACTORY_MCP_SECRET_<NAME>}`), never through the launch line or a command line.

#### Scenario: Denied tool
- **WHEN** `appstoretracker` denies `add_to_watchlist`
- **THEN** the runtime's disallowed tools SHALL include `mcp__appstoretracker__add_to_watchlist` and its MCP servers SHALL be `factory` and `appstoretracker`
