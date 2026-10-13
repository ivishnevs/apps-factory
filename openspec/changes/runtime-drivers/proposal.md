## Why
The factory runs two runtimes: Codex through the Go worker (`internal/worker`, app-server RPC pinned to one exact version) and Claude through the TypeScript Agent SDK worker (`workers/claude`). Both honour the same Core contract, but the run lifecycle (claim, lease, heartbeat, workspace, credential, events) is implemented twice in two languages, and their capabilities diverge silently: neither reports usage or cost in a normalized form, a Claude subscription limit surfaces as an ordinary FAILED turn that burns retries, the Claude version is pinned only by the lockfile with no runtime probe, and neither runtime has an exit guard or command guards. Different models are worth mixing (for example a Claude developer reviewed by a Codex reviewer), and one provider's limit must not stop agents that use another.

## What Changes
- A **runtime-neutral `Driver` interface** in the Go node with declared **capabilities** (resume, browser, mcp, cost, activity, tool_policy, exit_guard), normalized events (session, activity, usage, cost, result, quota with reset time, auth error, max turns, classified error), a resume policy, guard application and skill materialization. The run lifecycle lives only in Go.
- **Codex driver**: the current app-server client moved behind the interface unchanged.
- **Claude driver**: the Go node launches a **per-run Agent SDK shim** (`workers/claude` reduced to its runtime adapter) speaking JSON lines: launch parameters in, normalized events out, interrupt in. The standalone TypeScript worker keeps running until `node-agent` replaces it. The SDK choice is recorded in design D3 (decided and verified live).
- Exit guard and command guards for both runtimes: Claude through SDK `Stop` and `PreToolUse` hooks, Codex through a follow-up turn and app-server approval requests plus PATH shims.
- **Supported version ranges and driver probes** (binary, version, authentication; no model call) at node start and periodically; a failing probe disables that driver on the node with one `driver_unsupported` alert; the node pauses only when no driver is usable.
- **Usage and cost** normalized and stored on the run until the cost ledger exists; sessions keyed by (task, agent), never resumed across nodes.
- **Quota keys per company**: a limit on one key pauses only the agents that use it, until the reset time, without counting attempts.
- Runtime, model and options per agent in YAML (existing `spec.runtime`), validated against driver capabilities and passed through without silent drops; an optional per-task model override.
- A fake driver that replays recorded streams for tests.

## Out of scope
A Python worker, other runtimes, money budgets, network sandboxes.

## Dependencies
`reliable-recovery` (outcomes incl. `paused_out`), `explicit-exits-authority` (exit guard, MCP adapter), `node-agent` (slots, driver registration, command guards).

## Capabilities
### New Capabilities
- `runtime-drivers`: Driver interface and capabilities, Codex and Claude drivers, version ranges, quota keys and normalized runtime events.

### Modified Capabilities
None (replaces the exact Codex version pin and the duplicated per-runtime worker lifecycle).

## Impact
New `internal/driver` (`driver.go`, `codex/`, `claude/` — shim launcher, `fake/`), `internal/worker` (uses `Driver`), `internal/rpc` (moved under the codex driver), `workers/claude` (becomes the SDK shim: `src/runtime.ts` kept, bus/claim/heartbeat code removed after `node-agent`), `internal/config` (`runtime.quota_key`, options validation), `internal/storage`/`internal/core` (`quota_keys`, pause, `quota_paused` escalation), `cmd/factory-node` (probes), docs `runtime-protocol.md`, `adding-runtime.md`, `claude-runtime.md`.
