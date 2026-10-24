## Why

Agents reach the world only through Factory MCP and their shell. Some roles need a remote MCP server with its
own credential: the tvorog Product Researcher needs App Store Tracker (revenue and download estimates, an HTTP
MCP server authenticated with `Authorization: Bearer <key>`). The key must stay on the node: not in the
catalog, Core, the run's config snapshot, events, logs or a repository.

## What Changes

- A company declares external MCP servers once in `mcp_servers.yaml` of its catalog directory (next to
  `quota_keys.yaml` and `skills.yaml`): name, `transport: http`, `url`, `headers`, `deny_tools`. Header values
  reference node secrets as `${secret:NAME}`.
- Agents opt in with `spec.mcp: [name, ...]` (merged as a set from profiles). Loading fails on an unknown
  server, an invalid URL, plain `http` to a non-loopback host, a credential-looking header (Authorization,
  *key*, *token*, …) whose value is not a `${secret:...}` reference, any other `${...}`, the reserved name
  `factory`, and on a codex agent with `spec.mcp`.
- The run's config snapshot carries the agent's server definitions with the references unresolved.
- `node.yaml` `secrets_file`: a KEY=VALUE file readable by its owner only (a group- or world-accessible file is
  refused). At each run that needs it the node resolves the references, adds the values to the run's redaction
  list (run log, events, result, report attachments) and fails the run's setup (`setup_failed`) naming the
  server and the missing secret, never a value.
- The Claude driver adds the servers to the runtime's MCP config (type `http`) next to `factory`, keeps
  `strictMcpConfig`, and adds `mcp__<server>__<tool>` for each denied tool to `disallowedTools`.
- Codex is out of scope: the catalog refuses it and the codex driver refuses a launch with external servers.

## Impact

- `internal/protocol/mcp.go`, `internal/config` (mcp.go, Spec.MCP, Agent.MCPServers), `internal/node`
  (secrets.go, `secrets_file`), `internal/worker` (external_mcp.go, execute), `internal/driver` (Launch.ExternalMCP,
  capability ExternalMCP), `internal/driver/claude`, `internal/driver/codex`, `workers/claude` (shim launch line,
  runtime options).
- Docs: `docs/agent-configuration.md`, `docs/factory-mcp.md`, `docs/claude-runtime.md`, `docs/development.md` (node.yaml).
- Not in scope: stdio servers, `allow_tools`, node advertisement of secret names or secret-aware scheduling
  (a node without the secret fails the run's setup), doctor reporting, Codex.
