# Adding a runtime

Keep RunSpec provider-neutral: identity, project/agent, workspace, prompt/instructions, timeout,
session ID and runtime_options. Core alone claims leases and updates persisted run state.

A runtime is a Go driver that runs inside the node (`factory-node run`); there are no per-runtime services.

1. Register the provider in `protocol.RuntimeProvider.Valid` (currently codex and claude).
2. Implement `internal/driver.Driver` in `internal/driver/<provider>` (Codex: `internal/driver/codex`, app-server
   RPC; Claude: `internal/driver/claude`, a per-run Node shim). `Launch` starts one runtime process (group) per run
   with `Launch.Env` as its whole environment and the provider-neutral `factory-mcp` stdio adapter from
   `Launch.MCP`; `Start` sends the prompt; `Events`/`Done` report normalized activity and terminal evidence
   (`Result.Status`, else `Result.Err`; classify unknown outcomes conservatively and name known ones with
   `driver.OutcomeError`, an auth failure with `driver.AuthFailure` or `Result.Auth`); `Interrupt` requests
   cancellation. Do not implement claim, lease, heartbeat, task orchestration or event publication in a driver.
3. `Probe`/`Installed`/`CheckVersion` (`node.RuntimeProbe`: binary, supported version, auth; never a model call)
   serve `factory-node doctor` and the node's periodic driver checks: a failing check, or a run ended by an auth
   failure, disables that driver on the node until a probe passes.
4. Add the driver to `node.yaml` `drivers` (`internal/node.Config`) and build it in `internal/node/runner`
   (`drivers`), and its probe in `cmd/factory-node doctor`. The node then fetches
   `factory.commands.agent.run.<provider>` with its own shared durable consumer.
5. Declare the runtime_options keys the driver reads; register them in `config.RuntimeOptions`, where config load
   rejects any other key. Keep provider settings inside runtime_options rather than adding SDK-specific fields to
   Core.
6. Add driver tests (recorded or scripted streams) and executor tests with `internal/driver/fake`.

The executor in `internal/worker` keeps claim, lease, heartbeat, workspace (lock, worktree, OpenSpec preflight),
the run-scoped credential from `/runs/{id}/credential` (owner and broker credentials never reach the runtime) and
event publication with correlation/causation IDs and lease, for every driver. `internal/driver/fake` replays
scripted sessions for executor tests (an activity with `tool` performs that Factory MCP call through `Launch.MCP`
with the run credential and records the response). Claude: see [claude-runtime.md](claude-runtime.md).
Factory MCP instructions come from the credential response, not from driver code. Task Engine creates the same durable
runs without a runtime SDK dependency. Preserve task_id, attempt and configuration snapshots
when adding providers; Core projects terminal run outcomes into task state transactionally.

Message delivery runs carry message_id without task_id. Preserve this context and let Core manage
blocking/answers and continuation. A provider must honor the same opaque session_id resume contract
for task continuations and owner conversations; no messaging scheduler belongs in a driver.
