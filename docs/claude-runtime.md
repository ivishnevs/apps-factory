# Claude runtime — Agent SDK Worker

`workers/claude` is a native TypeScript Worker built on `@anthropic-ai/claude-agent-sdk` 0.3.284
(bundled Claude Code 2.1.284). It is pinned exactly in `package-lock.json`; upgrading requires
rerunning `npm test` and the live checks below. The SDK owns the agent loop; the Worker owns only the
broker transport, lease lifecycle and classification of terminal evidence.

Agents select it with `spec.runtime.provider: claude` (see `configs/agents/claude-demo.yaml`).
`runtime.model: default` leaves the model to Claude Code; any other value is passed as `model`.
`runtime_options.effort` (`low|medium|high|xhigh|max`) and `runtime_options.max_turns` (positive
integer, SDK `maxTurns`) are forwarded when present.

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

## Per-run shim (`src/shim.ts`)

`internal/driver/claude` (runtime-drivers D3) starts one `node workers/claude/dist/src/shim.js` per run.
It runs the same `src/runtime.ts` as the standalone Worker, without bus, claim and heartbeat code; the
standalone service keeps its own lifecycle until `node-agent` lands.

- stdin, first line: launch parameters `{"spec": RunSpec, "cwd", "instructions", "mcp_command"?,
  "executable"?, "allow_api_key"?, "setting_sources"?}`. `instructions` is the full appended system
  prompt (agent instructions plus Factory MCP instructions).
- stdin, then: `{"start": true}` delivers the prompt. The shim writes `session` after the startup
  gate and waits for it, so the driver persists the session before the turn executes.
  `{"interrupt": true}` requests `interrupt()`. EOF on stdin (driver gone) and SIGINT/SIGTERM
  interrupt too, so the driver keeps stdin open for the whole run. Interruption before `start`
  ends the run without a prompt (`error{class: not_started}`, `result` UNKNOWN).
- Environment: `FACTORY_URL` and `FACTORY_RUN_TOKEN` come only from the shim's environment and reach
  `factory-mcp` by inheritance; with `mcp_command` set a missing `FACTORY_RUN_TOKEN` is a launch error.
  Owner/broker secrets and (unless `allow_api_key`) API keys are dropped as in the Worker.
  `CLAUDE_CODE_EXECUTABLE` applies when the launch has no `executable`.
- stdout: one JSON event per line, discriminated by `event`; stderr carries diagnostics only.

| Event | Source |
| --- | --- |
| `session{id}` | pre-assigned or resumed session, written before the prompt is delivered (waits for `start`) |
| `activity{kind, data}` | every SDK message; `kind` is the progress `method`, `data` the redacted message |
| `quota{resets_at?, rate_limit_type?}` | `rate_limit_event` with `rate_limit_info.status: rejected` |
| `auth_error{reason, api_key_source?}` | `apiKeySource` other than `none` without `allow_api_key`; assistant `error` of `authentication_failed`, `oauth_org_not_allowed`, `account_on_hold`, `verification_required` |
| `usage{model, input, output, cache_read, cache_write}` | `result.modelUsage`, one line per model, cumulative for the query |
| `cost{usd, source: "claude_estimate"}` | `result.total_cost_usd`, cumulative |
| `max_turns{num_turns}` | FAILED result with subtype `error_max_turns` |
| `error{class, terminal_reason?, api_error_status?, message?}` | other FAILED results (`class` = result subtype, `api_error` for `success` with `is_error`), or a runtime failure (`launch`, `executable`, `startup_timeout`, `mcp_unavailable`, `session_mismatch`, `no_result`, `interrupt_timeout`, `interrupt_unknown`, `not_started`, `runtime`) |
| `result{status, result, session_id?}` | always the last line, exactly once; status as in the table above (UNKNOWN after any runtime failure) |

Classification uses structured SDK fields only, never message text. The shim exits 0 after the
`result` line. `shim.js --probe` prints one `probe{claude_code_version, sdk_version, account}` line
(`claude --version` of the bundled or `CLAUDE_CODE_EXECUTABLE` binary plus `accountInfo()` on an
empty prompt stream, no model call) and exits 0, or an `error{class}` line and exits 1.

Recorded SDK streams live in `internal/driver/claude/testdata`: `<name>.json` (launch line,
environment, account, MCP status sequence, interrupt point), `<name>.sdk.jsonl` (SDK messages in
delivery order, shaped on SDK 0.3.284 types) and `<name>.events.jsonl` (expected shim stdout, also
usable as fake shim output by Go driver tests). `test/shim.test.ts` replays start, resume, interrupt,
max turns and rate limit; `UPDATE_TESTDATA=1 npm test` regenerates the `.events.jsonl` files.

## Go driver (`internal/driver/claude`)

`claude.Driver` implements `driver.Driver` with the shim; it is not wired into `cmd/` yet (the
standalone service still serves `factory.commands.agent.run.claude`). Fields: `Node` (default
`node`), `Shim` (default `workers/claude/dist/src/shim.js`), `Executable`, `AllowAPIKey`,
`SettingSources` (launch line fields). The shim runs in its own process group (`ProcessGroup()`,
killed by `Close`) with `Launch.Env` as its whole environment; `Launch.MCP.Command` becomes
`mcp_command` and `MCP.EnvVars` reach `factory-mcp` by inheritance.

- `Launch` validates `runtime_options` (`model`, `effort`, `max_turns`; anything else is refused),
  writes the launch line and returns at `session`. A failure before it is a `*claude.Error` with the
  shim's class (`auth_error` for `auth_error`).
- `Start` writes `{"start": true}`; `Interrupt` writes `{"interrupt": true}`.
- Events: `activity` → `EventActivity` with `Method` = `kind`; `usage`, `cost`, `quota`,
  `auth_error`, `max_turns` and `error` → `EventActivity` with `Method` `claude/shim/<event>` and the
  line as `Data`.
- `result` COMPLETED/FAILED/CANCELLED → `Result.Status` with `result` as `Text` (quota and max turns
  are FAILED); UNKNOWN, a broken line or exit without `result` → `Result.Err` (`*claude.Error`,
  class from the last `error`/`auth_error`, else `no_result`/`protocol`). After `result` the driver
  waits up to 10 s for the shim to exit so the transcript is flushed for resume.
- Skills: `Launch.Skills` (verified copies under `Launch.SkillsRoot`, see
  [development](development.md#node-process)) are copied into `<cwd>/.claude/skills/<name>` before the shim
  starts, where Claude Code reads project skills (`settingSources` must include `project`, the default). Each copy
  carries a `.factory-skill` marker with its hash, is listed in the repository's `info/exclude` (so neither the
  agent's commits nor the worktree seal include it) and is removed by `Close`; copies a crashed run left behind
  are replaced, a project's own skill with the same name refuses the launch.
- `ResumePolicy` resumes only a session whose prompt this node delivered (`driver.ResumeSameNode`); the
  executor clears `session_id` and prepends the fresh-session note otherwise.
- `CheckVersion` runs `shim.js --probe` and accepts SDK 0.3.284 with Claude Code 2.1.284.
- `Probe` (`factory-node doctor` and the node's periodic driver checks; fields `Host`, `Node`, `Shim`,
  `BinDir`, `Executable`) checks what `Launch` runs: Node 22+ (`claude.node`), the built shim
  (`claude.shim`, default `BinDir/../workers/claude/dist/src/shim.js`) and, through `shim.js --probe` (no
  model call), the supported SDK/Claude Code pair (`claude.version`) and the login from `accountInfo`
  (`claude.auth`: an API-key source fails unless API keys are allowed; `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`
  only warn, runs remove them). `Installed` is Node plus the shim.

Tests replay the recorded `.events.jsonl` streams through a fake shim process
(`claudetest.Serve`, re-executed test binary): start, resume, interrupt, max turns, rate limit,
failures without evidence, and the built shim's launch refusal when `workers/claude` is built.
`internal/worker` runs Claude runs end to end through the executor against a fake Core
(`NATS_TEST_URL`): completed and owner-cancelled.

## Clock

Run timeout, heartbeat and runtime deadlines (startup, interrupt, shutdown, MCP polling) use
`src/clock.ts`, a clock that excludes system sleep (`reliable-recovery` 3.4; outcome mapping is
unchanged). Node timers follow libuv `uv_now()`/`uv_hrtime()`: on Linux `CLOCK_MONOTONIC`, which
stops during suspend, so timers are used as is; on macOS `mach_continuous_time()`
(`CLOCK_MONOTONIC_RAW`), which keeps counting while asleep, so a run would time out right after a
long sleep. Off Linux the clock observes `process.hrtime` every second while a timer is pending and
does not count a jump above 5 s between observations; deadlines may be late by up to one tick per
sleep. `test/clock.test.ts` pins the `process.hrtime` source against the OS clocks on both
platforms. Per-request HTTP timeouts to Core stay on plain timers.

## Runtime settings

`permissionMode: bypassPermissions` mirrors Codex `danger-full-access` + `approvalPolicy never`;
no isolation is claimed. `AskUserQuestion` is disallowed: questions go through Factory MCP.
`settingSources: ['project']` loads workspace CLAUDE.md/settings but not user hooks or plugins.
`strictMcpConfig` ignores every MCP server except `factory`. Background tasks are disabled
(`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`): a turn ends only when its work ends.
Owner/broker secrets are removed from the runtime env; `FACTORY_RUN_TOKEN` reaches `factory-mcp` by
inheritance and is not part of the MCP configuration. Same-user processes can still read process
environments on macOS; this is the trusted host model, not a boundary.

Sessions are stored by Claude Code under `~/.claude/projects` per workspace path, so a session can
only be resumed on the node (and workspace path) that ran it; the Go executor resumes only sessions it
recorded in `<state_dir>/sessions` and starts others fresh. Core refuses an
explicit resume of a session created by another provider; scheduled task/conversation continuations
start a fresh session after an agent changes provider.

OpenSpec `factory_openspec_read` tool results are replaced with `{document_body_omitted: true}` in
progress events; event data above 256 KiB is replaced with a bounded preview.

## Verification

`npm test` (in `workers/claude`) covers the recorded shim streams, the sleep-excluding clock, the startup gate, API-key refusal, MCP readiness, session
mismatch, cancellation with and without evidence, classification, redaction and env scrubbing with a
fake `Query`. Verified live on 2026-10-13 against an isolated Core + NATS with Claude Code 2.1.284:
direct run COMPLETED, resume of the same session, cross-provider resume rejected (409),
owner cancellation CANCELLED in ~4s with `terminal_reason: aborted_streaming`, `factory_project_get`
through Factory MCP with the tool-call audit recorded.
