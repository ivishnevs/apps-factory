## Context

Claude runs go through the per-run Node shim and the Claude Agent SDK; the shim passes `mcpServers` with only
`factory` and `strictMcpConfig: true`, so no user or project MCP server leaks into runs. The SDK turns
`mcpServers` into `--mcp-config <json>` on Claude Code's command line, which any local user can read with `ps`.

## Decisions

### D1 Company file, agent opt-in
`mcp_servers.yaml` sits in the company's catalog directory (`configs/` for `default`, `configs/<company>/`), like
`quota_keys.yaml`; absent means no servers. `spec.mcp` lists server names and merges as a set through profiles
(like `labels`). The resolved agent carries `mcp_servers` (the definitions it opted in to), so the run's config
snapshot and config hash include them and the node needs nothing from the catalog.

### D2 Secrets are references until the node
Header values may hold `${secret:NAME}` (NAME: letters, digits, `_`). Any other `${` is refused, so nothing else
is expanded. A header whose name looks like a credential (auth, token, key, secret, cookie, password,
credential, session) must contain a reference: a literal there is a loading error. Core and the snapshot only
ever see the reference.

### D3 Node secrets file, read per run
`node.yaml` `secrets_file` (made absolute like `token_file`) is read at each run that needs a secret, so edits
apply without a restart. Format: `KEY=VALUE`, `#` comments, optional `export `, optional matching quotes. A file
whose mode grants anything to group or others is refused (`chmod 600`). Errors name the file, line, server and
secret, never a value. A missing file, missing key or empty value fails the run's setup with outcome
`setup_failed` before the runtime starts. Secret-aware scheduling (nodes advertising secret names) is
deliberately left out: the first deployment has one node.

### D4 The value reaches Claude Code through its environment, not argv
The node puts each value into the runtime environment as `FACTORY_MCP_SECRET_<NAME>` and rewrites the header to
`${FACTORY_MCP_SECRET_<NAME>}`. Claude Code expands `${VAR}` in `--mcp-config` server configs (dynamic scope,
`expandVars: true` in Claude Code 2.1.284), so the launch line and Claude Code's argv carry only the reference.
`FACTORY_MCP_SECRET_*` is in the runtime deny list so a node environment can never inject one. Trust model
unchanged: the agent runs as the node's user with full host access and can read its own environment; the
guarantee is that values do not leave the node through Factory (Core, events, logs, snapshot), not isolation
from the agent.

### D5 Redaction
Resolved values are added to the run's redactor before anything else is emitted, so the run log, events, the
result and the redaction file handed to the MCP adapter (report attachments) all replace them.

### D6 Claude only; deny list only
The Claude driver (capability `ExternalMCP`) sends `mcp_servers` (`type: http`, url, headers) and
`disallowed_tools` (`mcp__<server>__<tool>`) on the launch line; the shim merges them with `factory` (set last,
so it cannot be replaced) and `AskUserQuestion`. External servers are not `alwaysLoad` and are not waited for at
startup: a server that fails to connect leaves the agent without its tools, not without its run. `allow_tools`
is not offered: with `bypassPermissions`, Claude's allowedTools does not restrict, and a server-side allow list
would need tool discovery. Codex: refused at catalog load (`spec.mcp` on a codex agent) and by the codex driver.

### D7 Transport http; plain http only on loopback
Only `transport: http` (streamable HTTP). `https` is required except for a loopback host, which allows pointing
`url` at a local proxy. App Store Tracker answers JSON-RPC notifications (`notifications/initialized`) with HTTP
400; the MCP client in Claude Code may treat that as a failed connection. Whether it does is not verified (no
live runs); if the server shows as failed, run the existing local proxy (answers notifications with 202,
forwards the caller's Authorization header) and set `url: http://127.0.0.1:3310/mcp`.

## Risks

- A secret value shorter than 8 bytes is not redacted by exact match (`redact.MinValue`).
- The agent can print its environment; redaction catches the exact value in Factory's outputs only.
