## MODIFIED Requirements

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

### Requirement: Missing secret fails setup
A run whose servers need a secret that is missing in Core at claim (deleted after dispatch; before dispatch the task
waits, `core-secret-store`), or whose sealed value the node cannot open, SHALL end `setup_failed` before the runtime starts, with a reason naming the server and the secret and never a
value.

#### Scenario: Missing secret
- **WHEN** a run's server references `${secret:ASTR_API_KEY}` and the secret is deleted after the run was dispatched
- **THEN** the run SHALL end `setup_failed` with a reason naming `ASTR_API_KEY` and no runtime SHALL be launched

## REMOVED Requirements

### Requirement: Node secrets file
**Reason**: Customers set keys once on the website and Core delivers them to whichever of the company's nodes claims the run; Core is the only source of secrets (`core-secret-store`). Redaction of delivered values moves to `core-secret-store`.
**Migration**: None (not released). node.yaml `secrets_file` and `internal/node/secrets.go` are removed.
