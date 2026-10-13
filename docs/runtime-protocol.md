# Runtime protocol — Codex 0.162.1

Verified against installed `codex-cli 0.162.1`, official generated JSON schemas and
[Codex app-server documentation](https://developers.openai.com/codex/app-server).
Run `make schema` to regenerate `.cache/codex-schema`; generated schemas are not committed.
The executable enforces this version; upgrading requires regenerating schemas and rerunning tests.

Worker runs `codex app-server --listen stdio:// -c 'model_provider="openai"'` and exchanges
newline-delimited JSON RPC messages, never terminal UI or human-readable completion output.
App-server owns the internal agent loop. Worker owns only the RPC transport and run lifecycle.

Sequence: initialize with clientInfo → initialized notification → account/read → thread/start
(or thread/resume with excludeTurns) → turn/start. Core persists thread ID before turn execution.
account/read must report chatgpt; no API-key login or auth mutation is attempted. The child's
existing Codex profile supplies model/settings, with optional YAML model override. Host permission
mode is danger-full-access and approvalPolicy never, matching the trusted MVP runtime.

Worker reads responses by ID, forwards notifications as durable progress events, and extracts final
agentMessage text from item/completed. item/agentMessage/delta remains available in run events.
Only turn/completed status completed/failed/interrupted yields COMPLETED/FAILED/CANCELLED.
An interrupted turn is CANCELLED even when triggered by the execution timeout.
Unexpected process exit, protocol failure, lease loss, or missing terminal evidence yields UNKNOWN.
Stderr supplies native diagnostics; protocol messages are not parsed from stderr.

On cancellation Worker sends turn/interrupt, waits up to 10s for terminal evidence, then kills the
app-server process group on close. Runtime startup calls have 30s timeouts. Event queue is bounded;
overflow fails safely to UNKNOWN instead of silently discarding notifications.

Server-initiated command/file approval requests receive decline. Other requests (including
user input, external auth refresh and tools not implemented by Foundation) receive a JSON-RPC
error and stop the run as UNKNOWN for owner attention. This is a documented Foundation limitation,
separate from the Core [inbox/action approval workflow](approvals.md). Full access means shell actions can
bypass behavioral instructions; no claim of isolation is made.

NATS subjects: factory.commands.agent.run.codex; factory.events.agent.started/progress/completed/failed.
Durables: codex-worker-v1, core-events-v1. Provider suffix is a deliberate extension of the original
single command subject, enabling independent runtime consumers without Codex fields in RunSpec.
Cancellation currently uses Core's durable CANCELLING state + heartbeat response, not a second broker
consumer. It survives restarts. Task and message events are published through Core transactional outbox; approval subjects remain for a later stage.

Endpoints require Authorization: Bearer FACTORY_TOKEN:
GET /health, /agents, /runs, /runs/{id}, /runs/{id}/events;
POST /runs, /runs/{id}/claim, /runs/{id}/heartbeat, /runs/{id}/cancel, /runs/{id}/resolve.
Lease tokens and raw runtime events are internal trusted-component data; the API is not a public API.


Factory MCP adds per-process `mcp_servers.factory` CLI overrides (required stdio server with
FACTORY_RUN_TOKEN/FACTORY_URL env forwarding). Worker requests the scoped credential from
POST /runs/{id}/credential after claim; owner/broker secrets are scrubbed from the runtime env.
MCP tools run through the subprocess independently of app-server server-initiated RPC requests;
no custom tool loop was added. See factory-mcp.md for the full contract and trust boundary.
GET /runs/{id}/tool-calls is an owner-only audit endpoint. /agent/tools and /agent/call accept
only active run credentials, not the shared owner token.

Messaging communication runs carry message_id without task_id, reusing the same app-server RPC.
An owner conversation may supply session_id to resume; separate agent question runs start a fresh
thread. Blocking task continuations reuse their original task thread after explicit answers.
