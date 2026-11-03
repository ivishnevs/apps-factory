## Context
Two Workers exist. `internal/worker.execute` (Go) starts `codex app-server` over stdio (`internal/rpc`), performs initialize → account/read → thread/start|resume → turn/start, maps notifications to progress events and finishes on `turn/completed`; `cmd/codex-worker` refuses any version other than `codex-cli 0.162.1`. `workers/claude` (TypeScript) consumes `factory.commands.agent.run.claude`, claims through Core, holds the workspace through `bin/factory-workspace`, and drives the Claude Agent SDK `query()` in streaming-input mode (`src/runtime.ts`): startup gate (subscription auth, Factory MCP connected) before the prompt, pre-assigned session ID, `result` as the only terminal evidence, `interrupt()` for cancellation. Both receive the Factory MCP instructions with the run credential. RunSpec keeps provider-specific settings in `runtime_options`.

## Goals / Non-Goals
Goals: one implementation of the run lifecycle; adding a runtime touches no Core logic; the same MCP adapter, exit guard, guards and cost accounting for every runtime; explicit behaviour on version drift and quota limits.
Non-goals: a model loop of our own, moving claim/lease logic into TypeScript, API-key billing decisions (the owner decides per quota key).

## Decisions

### D1. Interface
```go
type Driver interface {
    Name() protocol.RuntimeProvider
    Capabilities() Capabilities // Resume, Browser, MCP, Cost, Activity, ToolPolicy, ExitGuard
    CheckVersion(ctx context.Context) (Version, error) // within the supported range
    Probe(ctx context.Context) []node.CheckResult      // implements node.RuntimeProbe: binary, version, auth; no model call
    Launch(ctx context.Context, l Launch) (Session, error)
    ResumePolicy(prev Outcome, sameNode bool) bool
}
type Launch struct { Spec protocol.RunSpec; Brief string; Cwd string; Env []string; MCP MCPConfig; Guards []policies.Rule; Skills []SkillRef; ResumeID string }
type Session interface { Events() <-chan Event; Interrupt(ctx context.Context) error; Done() <-chan Result }
```
Normalized `Event` kinds: `session{id}`, `activity{kind}`, `usage{model, input, output, cache_read, cache_write}`, `cost{usd, source}`, `result{status}`, `quota{quota_key, resets_at}`, `auth_error`, `max_turns`, `error{class}`. Errors are classified from structured runtime events and exit codes, never by parsing human-readable output. The slot executor maps them to outcomes (`reliable-recovery`) and reports the last usage/cost with progress events and the run end; Core stores it on the run (`runs.usage` jsonb) until the cost ledger (`waits-events-cost-cli`) turns it into ledger rows. An option the driver does not support is a validation error at config load, never silently dropped. Model and options come from the agent YAML; a task may carry a `model` override (set by an authority, validated against the assignee's provider when set); an override that does not fit the current assignee's provider (e.g. after reassignment) is ignored with a feed event.

Resume: the session key is (task, agent) (`reliable-recovery` D3). `ResumePolicy` returns false when the previous run was on another node (`sameNode = false`), after `reset-workspace`, or when the previous outcome says the session is unusable; the run then starts a fresh session whose brief carries the context.

### D2. Codex driver
`internal/driver/codex` = the current app-server client moved as is (handshake, threads, turns, interrupt, server-initiated requests, ChatGPT account check), plus the exit guard follow-up turn from `explicit-exits-authority`. Command guards for Codex: the node's PATH shims (`node-agent` D6) plus a mapping of app-server server-initiated approval requests (command execution, file change) onto the agent's policies: deny → decline with the rule's instruction, approval → accept only with a single-use exact grant, allow → accept. Capabilities: resume, mcp, activity, exit_guard (follow-up turn), tool_policy (approval-request mapping); cost only as tokens.

### D3. Claude driver: Agent SDK shim (decided)
Decision, verified live on 2026-10-13 with Agent SDK 0.3.284 (bundled Claude Code 2.1.284): the Claude runtime is driven through the **Agent SDK**, not the raw CLI stream. It gives what the driver needs as structured signals: `interrupt()` with a `result` carrying `terminal_reason: aborted_*` as cancellation evidence; `accountInfo()` and `mcpServerStatus()` control requests before the first prompt; a pre-assigned `sessionId` (so Core persists the session before the turn) and `resume`; `strictMcpConfig` and `settingSources` for isolation from user configuration; `Stop` and `PreToolUse` hooks; `rate_limit_event` with `status: rejected` and `resetsAt`; `result.modelUsage` and `total_cost_usd` for usage and cost.

`internal/driver/claude` launches one Node child per run: `node workers/claude/dist/src/shim.js` with the launch parameters as one JSON line on stdin, then reads normalized event lines from stdout and writes `{"interrupt": true}` to stdin on stop. The shim is `src/runtime.ts` without the bus, claim and heartbeat code; the run token reaches it and `factory-mcp` only through the environment. Until `node-agent` lands, the standalone `workers/claude` service keeps its own lifecycle (current behaviour); afterwards that code is removed and the shim is the only TypeScript left.

Either way the driver: implements `exit_guard` with a `Stop` hook that asks the adapter whether an exit was accepted and refuses to stop up to `exit_guard_retries`; applies command guards from policies in a `PreToolUse` hook (deny, or approval with a single-use exact grant, `node-agent` D6); keeps background tasks disabled (a turn ends only when its work ends); uses only the Factory MCP server and the agent's declared MCP servers; materializes skills pinned by a hash recorded on the run. Mapping: `rate_limit_event` rejected → `quota{resets_at}`; `apiKeySource` other than `none` without an API-key quota key → `auth_error`; `result` success → `result`; other results → classified `error` (`max_turns` from `error_max_turns`).

### D4. Version ranges
Each driver declares a supported range. Codex: from the verified 0.162.1 up to the next verified version. Claude: the SDK version pinned in `workers/claude/package-lock.json` and the Claude Code version it bundles (2.1.284 at the time of decision); `CLAUDE_CODE_EXECUTABLE` overrides are probed against the same range. Runtime checks live in the driver: `Probe` checks the binary, the version range and authentication without a model call (Claude: the shim's `--probe` mode reports the Claude Code version and `accountInfo`), and `node-agent` calls it at start and every 10 minutes, the version every hour. A failing probe disables the driver on that node: the node declares it unusable in its next heartbeat (no new epoch) and Core emits one `driver_unsupported{driver, check}` attention event; the driver is re-enabled when the probe passes. A probe failure is either definitive (not logged in, version outside the range, missing binary, Node or shim) or transient (the probe killed by its timeout, no output, a process that could not start for lack of resources; `CheckResult.Transient`, `node.TransientRun`): a live run on a loaded machine (an Xcode build and a test suite) had `shim.js --probe` killed by the 30 s timeout while a Claude run succeeded on the same node, and the node disabled Claude for a probe interval. Transient failures are retried within the check (node.yaml `probes.attempts`, default 3, `probes.retry_backoff` 5 s doubling, the command timeout doubled per attempt); a check still failing only transiently logs a warning and leaves the driver as it was, `probes.transient_failures` (default 3) such checks in a row disable it, and a run on the driver that succeeded or produced runtime events since the last check counts as health evidence, so a transient failure then never disables it. Definitive failures disable at once; the auth hold-off (3.2) is unchanged. Core still sees only usable/unusable transitions, so one `driver_unsupported` per real failure episode and none for transient ones. `node-agent` pauses the whole node only when no driver remains usable. Upgrading a range requires regenerating schemas/recorded streams and passing the driver's protocol tests.

### D5. Quota keys
`quota_keys(company_id, name, provider, paused_until, reason)`; agents reference one with `spec.runtime.quota_key` (default: the provider's key — the one key `configs/quota_keys.yaml` declares for the provider, otherwise `<provider>-default`). A `quota` event ends the run `paused_out` (no attempt, no budget), pauses the key until `resets_at` (or one hour if unknown) and opens one `quota_paused` escalation of scope quota key (no task triggers; clears itself at reset). `Evaluate` reports `paused:quota_key`. An `auth_error` or a missing/unsupported binary ends the run `paused_out` and disables that driver on that node (as a failing probe, D4) with one alert.

### D6. Fake driver
`internal/driver/fake` replays recorded event streams and scripted MCP calls (including exits) for unit, property and integration tests without a model.

## Risks / Trade-offs
- SDK and bundled Claude Code change between versions → exact lockfile pin, version range, recorded-stream tests per supported version.
- A Node child per Claude run adds a process and a startup cost → negligible next to model latency; the shim holds no state between runs.
- Two runtimes double the protocol surface → shared adapter, guard and accounting code; drivers stay thin.
- Subscription terms for automated use → the owner decides per quota key; the driver supports both subscription login and API key configuration.

## Amendments

- 2026-10-20: `spec.runtime.effort` (`low|medium|high|xhigh|max`, Codex and Claude) and `spec.runtime.max_turns` (Claude, at least 1) reach the run's `runtime_options` when set; the Codex driver reads `effort` and sends it on `turn/start` (20483a4).
- 2026-10-20 (resume): dispatch carries the assignee's own last session of the task, so a developer keeps its session across a reviewer of another provider (0355740; reliable-recovery D3 amendment).
- 2026-10-21 (Factory MCP schemas): Claude Code 2.1.284 defers MCP tools behind ToolSearch by default (tool search is on for first-party hosts). A live run's transcript showed every `mcp__factory__*` tool in a `deferred_tools_delta` with `defer_loading: true`; the model loaded five through ToolSearch and called the rest by name with guessed arguments (`factory_artifact_get {"id"}`, `factory_artifact_request_approval {"summary"}`), costing one 400 per missing field. The shim now declares `mcpServers.factory` with `alwaysLoad: true` (Agent SDK `McpStdioServerConfig.alwaysLoad`: "always included in the prompt and never deferred behind tool search"; Claude Code docs, MCP, "Exempt a server from deferral"). It is per server, so `ENABLE_TOOL_SEARCH` stays the owner's knob for everything else; its side effect (startup waits up to 5 s for the server) matches the shim, which already waits for `factory` to connect.
