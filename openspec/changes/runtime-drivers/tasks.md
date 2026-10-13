## 1. Interface and Codex
- [ ] 1.1 `internal/driver` interface, capabilities, normalized events, option validation at config load. Verify: config test rejects an unsupported option for a runtime.
- [ ] 1.2 Move the Codex app-server client into `internal/driver/codex` with no behaviour change; slot executor uses `Driver`. Verify: existing worker tests and `scripts/smoke.py` pass unchanged.
- [ ] 1.3 `internal/driver/fake` with recorded streams and scripted MCP calls. Verify: executor tests run entirely on the fake driver.

## 2. Claude
- [x] 2.1 Runtime decision: Agent SDK over raw CLI stream (design D3). Standalone `workers/claude` verified live: run, resume, cross-provider resume refusal, owner cancellation, Factory MCP call with audit.
- [ ] 2.2 Split `workers/claude` into the SDK shim (`src/shim.ts`: JSON-lines launch/events/interrupt, `--probe`) and the standalone service; record streams under `internal/driver/claude/testdata`. Verify: shim tests replay recorded SDK messages for start, resume, interrupt, max turns, rate limit.
- [ ] 2.3 `internal/driver/claude` launching the shim: resume/interrupt, MCP config, model/effort/max-turns options, skills materialization pinned by hash. Verify: executor tests on recorded streams; the Go slot executor runs a Claude run end to end against a fake Core.
- [ ] 2.4 Stop hook exit guard and PreToolUse command guards. Verify: a scripted session that tries to stop without an exit is refused twice then ends `no_exit`; a denied command is refused.
- [ ] 2.5 Usage/cost events for stopped and timed-out runs, stored on the run (`runs.usage`) until the cost ledger. Verify: interrupted recorded stream still yields usage on the run record.
- [ ] 2.6 Codex command guards: app-server approval requests mapped to deny/approval/allow. Verify: fake-RPC test with a denied, an approved-once and an allowed command.
- [ ] 2.7 `ResumePolicy` with (task, agent) session key: no resume across nodes or after reset-workspace; per-task model override validated against the provider. Verify: executor tests for same node (resume), other node (fresh); an override set for another provider is refused, and one left by a reassignment is ignored with an event.

## 3. Versions and quotas
- [ ] 3.1 `Probe` (binary, version range, auth) implementing `node.RuntimeProbe`; version checked at start and hourly; failing probe disables the driver with one `driver_unsupported` event and re-enables on pass. Verify: fake binary with an out-of-range version; fake auth failure on one of two drivers keeps the node claiming.
- [ ] 3.2 `quota_keys`, agent `quota_key`, pause until reset, `quota_paused` escalation, `paused:quota_key` reason; auth error pauses the node driver. Verify: quota event on key A leaves agents on key B running.

## 4. Docs and smoke
- [ ] 4.1 Update `docs/runtime-protocol.md`, `docs/adding-runtime.md`, `docs/claude-runtime.md`; remove the standalone Claude service once `node-agent` ships. Verify: `factory config validate`; `make check`.
- [ ] 4.2 Opt-in real smoke: Claude developer + Codex reviewer on one task. Verify: script records both sessions, exits and costs.
