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

## Core ↔ worker: additive fields (agreed 2026-10-14)

Optional fields; workers and Cores that do not know them ignore them.

- `RunSpec.repo_url`, `RunSpec.default_branch`, `RunSpec.workspace_mode` (`""`/`shared` = today's project
  workspace, `worktree` = a worktree of `repo_url` per task). Core fills them from the project (`repo_url`,
  `default_branch`) and the agent's `spec.workspace.mode`.
- `Event.usage`: array of `{model, input, output, cache_read, cache_write, cost_usd, cost_source}`, cumulative for
  the run, one entry per model. Core keeps the last usage it receives on the run (`runs.usage`), so a stopped or
  timed-out run reports usage on its terminal event. `cost_usd`/`cost_source` only when the runtime prices it
  (Claude: `claude_estimate`).
- `Event.quota`: `{resets_at, type}` when the runtime hits its subscription limit. Core pauses the agent's
  quota key (`spec.runtime.quota_key`; without one the provider's key from the snapshot's
  `spec.runtime.default_quota_key`: the provider's only declared key, else `<provider>-default`) until
  `resets_at` (unknown: a default period)
  and opens one `quota_paused` escalation for the key; the run's own outcome is reported as usual.
- RunSpec: `config_snapshot.skill_hashes` (`{name: "sha256:…"}`) pins the content of the offered skills
  (`skills.Hash` over Core's registry at dispatch); the node refuses the run before the runtime starts when its
  copy differs. `runtime_options.model` is the task's model override when it fits the provider, else the
  agent's. `workspace_mode` is `shared`, `worktree`, `fresh_from_default` or `scratch`.
- `POST /runs/{id}/workspace` `{lease, mode, branch, node_id, exit_sha, sealed_head, sealed_at}`: the worker reports
  a task run's workspace (`mode` shared|worktree|scratch; SHAs are full object IDs). The run's lease authenticates
  it, also after the run ended, so the seal that follows the end can be reported. Core keeps one row per task
  (`workspaces`); `GET /tasks/{id}/workspace` reads it.
- Projects take `repo_url` (https, ssh, file, scp-style or absolute path) and `default_branch`
  (`factory projects create ID WORKSPACE --repo URL --branch BRANCH`, `PATCH /projects/{id}`). Core sets
  `workspace_mode` only when the project has `repo_url`; otherwise the run keeps the shared workspace.
- `GET /runs/{id}` and task attempts return `usage`; `factory show` prints tokens and the runtime's cost estimate.
- `Event.outcome` (reliable-recovery D1), on terminal events only: how the run ended when the status does not
  say it — `failed`, `timed_out`, `idle_timeout`, `max_turns`, `setup_failed` or `paused_out`; other values
  are refused (`lost`, `revoked`, `stale`, `unclaimed`, `succeeded` and `no_exit` are Core's). A failure
  outcome (`failed`, `timed_out`, `idle_timeout`, `max_turns`, `setup_failed`) on a `CANCELLED` event ends the
  run `FAILED`, so a worker that interrupts its own run on a hard timeout reports
  `{status: CANCELLED, outcome: timed_out}` and the task fails retryably instead of being cancelled. Without
  an outcome Core derives one from the status and result (see task-lifecycle.md, Run outcomes).
- Idle stop: a started run without any applied event for 30 minutes (judged after a full window of
  continuous Core ticking, so a host sleep does not count) is asked to stop — v1: `{"cancel": true}` in the
  per-run heartbeat answer, v2: heartbeat `stop` with reason `idle_timeout`. The worker interrupts it as for
  a cancel; its `CANCELLED` end is recorded `FAILED` with outcome `idle_timeout`, and the task is not
  cancelled.

### Usage and quota sources

- Executor: keeps the run's latest usage per model and its latest quota hit. Each usage/cost report becomes a
  progress event carrying the run's usage so far; a quota hit becomes a progress event with `quota`. Every
  terminal event (`completed`, `failed` including `UNKNOWN`, and the one after an interrupt without a result)
  carries both. A run total cost (driver cost event) is set on the single model when no model has its own price.
- Codex: the model comes from the `thread/start`/`thread/resume` response. Usage comes from
  `thread/tokenUsage/updated` for this run's turn: `total` is thread-wide, so the first update is a baseline
  and later ones are reported relative to it. `input` = `inputTokens − cachedInputTokens`,
  `cache_read` = `cachedInputTokens`, `cache_write` = `cacheWriteInputTokens`; reasoning tokens are inside
  `output`. No cost. A final `usageLimitExceeded` turn error (an `error` notification with `willRetry=false`,
  or a failed `turn/completed`) is a quota hit (method `codex/quota`): `type` is `rateLimitReachedType`,
  `resets_at` is the latest reset of an exhausted window in the merged `account/rateLimits/updated`
  snapshots, unknown without one. `rateLimitExceeded` is a transient API limit, not a quota hit.
- Claude: the shim reports running usage per model after each assistant message (the last report per
  message id, `<synthetic>` skipped), so a killed or timed-out run still has usage; the result's `modelUsage`
  gives final usage with per-model `cost_usd` (`claude_estimate`), then the total cost. A rate-limit event
  that rejects the request is a quota hit with the limit's reset time and type (e.g. `five_hour`).

### Run outcomes from workers

How the executor (v1 and v2, every driver) fills `Event.outcome` on terminal events
(reliable-recovery 3.4); progress and `started` events never carry one.

| How the run ended | Terminal event |
|---|---|
| Turn completed (also when it finished as an interrupt was requested) | `COMPLETED` |
| Turn failed | `FAILED` `failed` |
| Claude `error_max_turns` result (shim `max_turns`) | `FAILED` `max_turns` |
| Turn failed after a quota hit (`Event.quota` in this run) or a Claude auth error | `FAILED` `paused_out` (an auth error also disables the driver on the node until its next probe passes) |
| The executor's own hard timeout (`timeout_seconds`, sleep-excluding clock) interrupted the turn | `CANCELLED` `timed_out` |
| v2 heartbeat `stop` `timed_out` / `idle_timeout` interrupted the turn | `CANCELLED` `timed_out` / `idle_timeout` |
| Owner cancel: v1 heartbeat `{cancel: true}`, v2 `stop` `revoked`, `terminal`, `unknown_run` | `CANCELLED` |
| Timeout or idle stop, no result within the interrupt deadline (the executor then kills the runtime) | `FAILED` `timed_out` / `idle_timeout` |
| Before the runtime started: invalid RunSpec, workspace allocation, `workspace_diverged`, OpenSpec preflight, skills refusal, shims or `factory-mcp` missing, run credential | `FAILED` `setup_failed` |
| The same, while the run was being stopped | `CANCELLED` with the stop's outcome |
| Launch refused before the prompt (Claude auth, Codex without a ChatGPT login, missing or unsupported runtime executable) | `FAILED` `paused_out` |
| Other launch, `/start` or `started` failures before the prompt; a broken shim stream | `FAILED` `failed` |
| No terminal evidence: interrupt for a revoke without a result, shim or app-server gone mid-turn, `turn/start` failure, event publication failure | `UNKNOWN` |

- A v1 idle stop is a plain `{cancel: true}`: the worker cannot tell it from an owner cancel and reports
  `CANCELLED`; Core records `idle_timeout` from its own stop reason. v2 carries the reason in `stop`.
- UNKNOWN stays where the runtime may still have acted after the last evidence and nothing names the cause
  (Core decides `lost`). A quota hit alone does not make an evidence-less end known.
- A lost v1 heartbeat or a worker/node shutdown interrupts like an owner cancel (`CANCELLED`, no outcome).
- Git lock contention inside worktree allocation (`index.lock`, `config.lock`, `packed-refs.lock`, ref locks) is
  retried by the allocator; a failure after the retries is transient contention, reported `FAILED` `failed`
  (`CANCELLED` while the run was being stopped), not `setup_failed` (task-worktrees D1).
- After an accepted exit (`exit_state: accepted`) a run the executor stopped (`ExitGrace` 30 s after the exit,
  its timeout, a stop) or that ended without terminal evidence reports `COMPLETED` without an outcome: the exit
  stands and Core records the run `succeeded`.
