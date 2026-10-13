# Claude runtime — Agent SDK Worker

`workers/claude` is a native TypeScript Worker built on `@anthropic-ai/claude-agent-sdk` 0.3.284
(bundled Claude Code 2.1.284). It is pinned exactly in `package-lock.json`; upgrading requires
rerunning `npm test` and the live checks below. The SDK owns the agent loop; the Worker owns only the
broker transport, lease lifecycle and classification of terminal evidence.

Agents select it with `spec.runtime.provider: claude` (see `configs/agents/claude-demo.yaml`).
`runtime.model: default` leaves the model to Claude Code; any other value is passed as `model`.
`runtime_options.effort` is forwarded when present.

## Running

```sh
make build            # Go binaries + npm ci + tsc
claude                # once: /login with a Claude subscription
./bin/factory config validate
make claude-worker    # node workers/claude/dist/src/main.js
```

Environment matches the Codex Worker: `FACTORY_TOKEN`, `FACTORY_URL`, `NATS_URL`.
Optional: `FACTORY_BIN_DIR` (location of `factory-mcp`/`factory-workspace`), `CLAUDE_CODE_EXECUTABLE`
(use another Claude Code binary), `CLAUDE_SETTING_SOURCES` (comma list, default `project`),
`CLAUDE_ALLOW_API_KEY=1` (otherwise API-key auth is refused and `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`
are removed from the runtime environment).

## Lifecycle

Subject `factory.commands.agent.run.claude`, durable `claude-worker-v1`, one unacknowledged command.
Fetch → claim → ack → execute, exactly as `internal/worker`. Events use the same envelopes, subjects,
lease and causation IDs; progress `method` is `claude/<type>[/<subtype>]` and `data` is the SDK message.

1. `bin/factory-workspace` acquires the canonical flock and runs the OpenSpec preflight (shared Go code).
   It holds the lock until its stdin closes, so a crashed Worker releases it with the pipe.
2. Heartbeat every 10s carries cancellation; a failed heartbeat stops the run. Execution timeout aborts.
3. `POST /runs/{id}/credential` returns the run token and the Factory MCP instructions.
4. `query()` starts in streaming-input mode. Before the prompt is delivered, the Worker checks
   `accountInfo().apiKeySource` (subscription login reports `none`) and waits until the `factory`
   MCP server is `connected`. Only then is `started` published with the session ID: new sessions get a
   pre-assigned UUID (`sessionId`), continuations use `resume`. The `system/init` session must match.
5. The `result` message is the only terminal evidence:

| SDK result | Run status |
| --- | --- |
| `success`, `is_error: false` | COMPLETED |
| any other result | FAILED (retryable within `max_retries`) |
| any non-success result after a requested `interrupt()` | CANCELLED |
| no result, process exit, protocol/startup error, no result within 10s of interrupt | UNKNOWN |

After the result the Worker closes input and drains briefly so the transcript is flushed for resume.

## Runtime settings

`permissionMode: bypassPermissions` mirrors Codex `danger-full-access` + `approvalPolicy never`;
no isolation is claimed. `AskUserQuestion` is disallowed: questions go through Factory MCP.
`settingSources: ['project']` loads workspace CLAUDE.md/settings but not user hooks or plugins.
`strictMcpConfig` ignores every MCP server except `factory`. Background tasks are disabled
(`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`): a turn ends only when its work ends.
Owner/broker secrets are removed from the runtime env; `FACTORY_RUN_TOKEN` reaches `factory-mcp` by
inheritance and is not part of the MCP configuration. Same-user processes can still read process
environments on macOS; this is the trusted host model, not a boundary.

Sessions are stored by Claude Code under `~/.claude/projects` per workspace path. Core refuses an
explicit resume of a session created by another provider; scheduled task/conversation continuations
start a fresh session after an agent changes provider.

OpenSpec `factory_openspec_read` tool results are replaced with `{document_body_omitted: true}` in
progress events; event data above 256 KiB is replaced with a bounded preview.

## Verification

`npm test` (in `workers/claude`) covers the startup gate, API-key refusal, MCP readiness, session
mismatch, cancellation with and without evidence, classification, redaction and env scrubbing with a
fake `Query`. Verified live on 2026-10-13 against an isolated Core + NATS with Claude Code 2.1.284:
direct run COMPLETED, resume of the same session, cross-provider resume rejected (409),
owner cancellation CANCELLED in ~4s with `terminal_reason: aborted_streaming`, `factory_project_get`
through Factory MCP with the tool-call audit recorded.
