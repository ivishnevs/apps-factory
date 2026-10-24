## 1. Catalog
- [x] 1.1 `mcp_servers.yaml` per company, `spec.mcp` opt-in merged as a set, resolved `mcp_servers` in the agent snapshot with references unresolved. Done: `internal/config/mcp.go`, `internal/protocol/mcp.go`. Verify: `go test ./internal/config -run MCPServers` (`TestMCPServers`).
- [x] 1.2 Validation: unknown server, bad URL, remote plain http, non-http transport, reserved `factory`, bad tool name, literal credential header, foreign `${...}`, codex agent with `spec.mcp`. Done: `TestMCPServersInvalid`.

## 2. Node
- [x] 2.1 `node.yaml` `secrets_file`: KEY=VALUE, owner-only mode, errors without values. Done: `internal/node/secrets.go`, `TestReadSecrets`.
- [x] 2.2 Resolve per run, `FACTORY_MCP_SECRET_<NAME>` in the runtime environment, header rewritten to `${FACTORY_MCP_SECRET_<NAME>}`, values redacted, missing secret / file / driver support is `setup_failed` naming the secret. Done: `internal/worker/external_mcp.go`; `TestExternalMCPResolve`, `TestJetStreamExternalMCPRun` (a command printing the secret: redacted in the run log and events; a node without it: `setup_failed`, no launch).

## 3. Drivers
- [x] 3.1 Claude: launch line `mcp_servers` + `disallowed_tools`; shim merges them with `factory` (set last) and `AskUserQuestion`, keeps `strictMcpConfig`. Done: `TestExternalMCPLaunch` (Go, no secret in the launch line), shim test "external MCP servers join factory…". Verify: `cd workers/claude && npm run build && npm test`.
- [x] 3.2 Codex: driver refuses `ExternalMCP` (`TestExternalMCPRefused`); catalog refuses codex agents with `spec.mcp`.

## 4. Docs and config
- [x] 4.1 `docs/agent-configuration.md` (mcp_servers.yaml, spec.mcp), `docs/factory-mcp.md`, `docs/claude-runtime.md`, `docs/development.md` (node.yaml `secrets_file`).
- [x] 4.2 tvorog: App Store Tracker in the company's `mcp_servers.yaml`, product-researcher `mcp: [appstoretracker]`, `deny_tools: [add_to_watchlist]`. Done: snippets handed to the integrator (configs/tvorog is not tracked in the repository).

## 5. Live
- [ ] 5.1 Owner: key in the node's secrets file, `secrets_file` in node.yaml, refreshed node and shim, one Product Researcher run shows `appstoretracker` connected (else switch `url` to the local proxy, design D7).
