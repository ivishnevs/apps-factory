# Claude runtime — Go driver and Agent SDK shim

Claude runs inside the node (`factory-node run`) as the Go driver `internal/driver/claude` (runtime-drivers D3),
which starts one per-run shim, `workers/claude/dist/src/shim.js`, built on `@anthropic-ai/claude-agent-sdk` 0.3.284
(bundled Claude Code 2.1.284). The SDK is pinned exactly in `package-lock.json`; upgrading requires rerunning
`npm test` and the live checks below. The SDK owns the agent loop; the shim owns the startup gate, event
normalization and the hooks; the node's executor (`internal/worker`, shared with Codex) owns claim, lease,
heartbeat, workspace, credential and events. The standalone TypeScript service (`src/main.ts`, `src/worker.ts`,
`bin/factory-workspace`) was removed (runtime-drivers 4.1); `workers/claude` is only the shim.

Agents select it with `spec.runtime.provider: claude` (see `configs/agents/claude-demo.yaml`).
`runtime.model: default` leaves the model to Claude Code; any other value is passed as `model`.
`runtime.effort` (`low|medium|high|xhigh|max`) and `runtime.max_turns` (positive integer, SDK `maxTurns`)
travel as `runtime_options.effort` / `runtime_options.max_turns` and are forwarded when set. A model newer than
the pinned Claude Code (e.g. `claude-haiku-5-5`) is passed by its full id; the aliases (`haiku`, …) resolve to
what that Claude Code version knows.

### Personal subscription (`CLAUDE_CONFIG_DIR`)

Claude Code keeps its login, settings and sessions in `CLAUDE_CONFIG_DIR` (default `~/.claude`). To run the
node's Claude agents on another account, such as the owner's personal subscription kept in
`~/.claude-personal`, start the node (and its doctor) with that variable:

```sh
CLAUDE_CONFIG_DIR=$HOME/.claude-personal ./bin/factory-node doctor -config data/node.yaml -drivers claude
CLAUDE_CONFIG_DIR=$HOME/.claude-personal ./bin/factory-node run -config data/node.yaml
```

`CLAUDE_*` is on the runtime environment allowlist (`worker.RuntimeEnv`), and the shim's environment scrub keeps
it, so the probe (`shim.js --probe`, which inherits the node's environment) and every run's Claude Code use the
same directory: `claude.auth` in `doctor` reports that account's login, and sessions are stored (and resumed)
under `$CLAUDE_CONFIG_DIR/projects`. Log in once with `CLAUDE_CONFIG_DIR=$HOME/.claude-personal claude` (`/login`).
Changing the variable between node starts makes earlier sessions unavailable for resume (they start fresh).

## Running

```sh
make build            # Go binaries + npm ci + tsc (builds the shim)
claude                # once: /login with a Claude subscription
./bin/factory config validate
mkdir -p data && cat > data/node.yaml <<'EOF'   # data/ is gitignored
drivers:
  claude: {enabled: true}   # codex stays enabled by default; {enabled: false} for a Claude-only node
EOF
./bin/factory-node doctor -config data/node.yaml -drivers claude
./bin/factory-node run -config data/node.yaml
```

Environment as for every node: `FACTORY_TOKEN`, `FACTORY_URL`, `NATS_URL` (see
[development](development.md#node-process)). `drivers.claude` keys: `node` (default `node`), `shim` (default
`<bin_dir>/../workers/claude/dist/src/shim.js`), `executable` (another Claude Code binary; else
`CLAUDE_CODE_EXECUTABLE`), `allow_api_key` (otherwise API-key auth is refused and
`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` are removed from the runtime environment). The driver is off by
default: enable it on nodes logged in to Claude. The node fetches `factory.<company>.commands.agent.run.claude`
(durable `claude-worker-<company>`) like any driver subject.

## Lifecycle

The executor runs a Claude run exactly like a Codex run (see [development](development.md#node-process)):
claim, ack, workspace or worktree, run credential with the Factory MCP instructions, `Launch`, `POST
/runs/{id}/start` (v2), `Start`, events, terminal event. Progress `method` is `claude/<type>[/<subtype>]`
and `data` is the SDK message. Inside the shim:

1. `query()` starts in streaming-input mode. Before the prompt is delivered, the shim checks
   `accountInfo().apiKeySource` (subscription login reports `none`) and waits until the `factory`
   MCP server is `connected`. Only then is `session` written: new sessions get a
   pre-assigned UUID (`sessionId`), continuations use `resume`. The `system/init` session must match.
2. The `result` message is the only terminal evidence:

| SDK result | Shim `result` status | Run outcome |
| --- | --- | --- |
| `success`, `is_error: false` | COMPLETED | `succeeded` |
| any other result | FAILED | `failed` (an attempt; the task retries after a backoff) |
| any non-success result after a requested `interrupt()` | CANCELLED | the stop's (`timed_out`, `idle_timeout`, `revoked`; `lost` for a shutdown) |
| no result, process exit, protocol/startup error, no result within 10s of interrupt | UNKNOWN | `lost` (no terminal evidence) unless the error class names it |

After the result the shim closes input and drains briefly so the transcript is flushed for resume. The
executor refines the outcome (`max_turns`, `paused_out` for quota and auth, `timed_out`, see
[runtime protocol](runtime-protocol.md)). A run that ends on an authentication failure also disables the
Claude driver on that node until its next probe passes (runtime-drivers 3.2).

## Per-run shim (`src/shim.ts`)

`internal/driver/claude` (runtime-drivers D3) starts one `node workers/claude/dist/src/shim.js` per run.
It runs `src/runtime.ts` (the SDK adapter); bus, claim and heartbeat belong to the Go executor.

- stdin, first line: launch parameters `{"spec": RunSpec, "cwd", "instructions", "mcp_command"?,
  "executable"?, "allow_api_key"?, "setting_sources"?, "exit_file"?, "exit_guard_retries"?,
  "guard_command"?}`. `instructions` is the full appended system prompt (agent instructions plus Factory
  MCP instructions). `exit_file`/`exit_guard_retries` turn on the exit guard (a `Stop` hook refuses a stop
  without an accepted or pending exit at most that many times); `guard_command` turns on the command guard.
- Command guard (runtime-drivers D2): with `guard_command` (the run's `factory-shim`) a `PreToolUse` hook
  (matcher `Bash|Monitor`) sends every shell command line to `guard_command --decide` (stdin
  `{"command"}`, stdout `{"decision": "allow"|"deny", "reason"?}`) with the shim's own environment
  (`FACTORY_SHIM_DIR`, `FACTORY_URL`, `FACTORY_RUN_TOKEN`; a missing `FACTORY_SHIM_DIR` is a launch error).
  `factory-shim` judges the line exactly like the Codex driver (see [policies](policies.md#command-guards)):
  deny → the tool call is denied (`permissionDecision: deny`) with the rule's instruction as the reason the
  model sees; approval → allowed only after Core consumed a single-use grant for that exact argv, which is
  handed to the PATH shim (`$FACTORY_SHIM_DIR/grants`) so the shimmed binary runs once without asking again;
  allow → the call passes unchanged. A failing or timed-out decision (30 s) denies. The PATH shims stay in
  place as the backstop (an absolute path or another tool bypasses the hook, not the shim).
- stdin, then: `{"start": true}` delivers the prompt. The shim writes `session` after the startup
  gate and waits for it, so the driver persists the session before the turn executes.
  `{"interrupt": true}` requests `interrupt()`. EOF on stdin (driver gone) and SIGINT/SIGTERM
  interrupt too, so the driver keeps stdin open for the whole run. Interruption before `start`
  ends the run without a prompt (`error{class: not_started}`, `result` UNKNOWN).
- Environment: `FACTORY_URL` and `FACTORY_RUN_TOKEN` come only from the shim's environment and reach
  `factory-mcp` by inheritance; with `mcp_command` set a missing `FACTORY_RUN_TOKEN` is a launch error.
  Owner/broker secrets and (unless `allow_api_key`) API keys are dropped defensively.
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

`claude.Driver` implements `driver.Driver` with the shim; `factory-node run` builds it from `drivers.claude`
(`internal/node/runner`) and `factory-node doctor` probes it. Fields: `Node` (default
`node`), `Shim` (default `workers/claude/dist/src/shim.js`), `Executable`, `AllowAPIKey`,
`SettingSources` (launch line fields). The shim runs in its own process group (`ProcessGroup()`,
killed by `Close`) with `Launch.Env` as its whole environment; `Launch.MCP.Command` becomes
`mcp_command` and `MCP.EnvVars` reach `factory-mcp` by inheritance. Capabilities include `ToolPolicy` and
`ExitGuard`: `Launch.ExitFile`/`ExitGuardRetries` become `exit_file`/`exit_guard_retries`, and when
`Launch.Guards` deny or gate a command the factory-shim behind the run's `FACTORY_SHIM_DIR` (the target of its
command links, `shim.Executable`) becomes `guard_command`; guards without a shim directory refuse the launch.

- `Launch` validates `runtime_options` (`model`, `effort`, `max_turns`; anything else is refused),
  writes the launch line and returns at `session`. A failure before it is a `*claude.Error` with the
  shim's class (`auth_error` for `auth_error`). Auth classes (`auth_error`, `auth`) are `driver.AuthFailure`s,
  and a result after an `auth_error` carries it in `Result.Auth`: the node disables the driver on them.
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
`TestBuiltShimDecider` runs the built shim's decider against a real `factory-shim --decide` and a fake Core
(deny, allow, one grant consumed and handed over, then refused). `internal/worker` runs Claude runs end to
end through the executor against a fake Core (`NATS_TEST_URL`): completed and owner-cancelled.
`internal/node/runner` (`NATS_TEST_URL`, real Core and node) verifies with the fake driver that a session
refused `ExitGuardRetries` stops without an exit ends with Core recording `no_exit` and a `no_exit`
escalation, and that an approval-gated command runs through `factory-shim` once after the owner's grant.

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

Sessions are stored by Claude Code under `$CLAUDE_CONFIG_DIR/projects` (default `~/.claude/projects`) per workspace path, so a session can
only be resumed on the node (and workspace path) that ran it; the Go executor resumes only sessions it
recorded in `<state_dir>/sessions` and starts others fresh. Core refuses an
explicit resume of a session created by another provider; scheduled task/conversation continuations
start a fresh session after an agent changes provider.

OpenSpec `factory_openspec_read` tool results are replaced with `{document_body_omitted: true}` in
progress events; event data above 256 KiB is replaced with a bounded preview.

## Verification

`npm test` (in `workers/claude`) covers the recorded shim streams, the sleep-excluding clock, the startup gate, API-key refusal, MCP readiness, session
mismatch, cancellation with and without evidence, classification, redaction and env scrubbing with a
fake `Query`. Verified live on 2026-10-13 (with the since-removed standalone service, which ran the same
`src/runtime.ts`) against an isolated Core + NATS with Claude Code 2.1.284:
direct run COMPLETED, resume of the same session, cross-provider resume rejected (409),
owner cancellation CANCELLED in ~4s with `terminal_reason: aborted_streaming`, `factory_project_get`
through Factory MCP with the tool-call audit recorded.
