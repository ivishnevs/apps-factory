## 1. Exits
- [ ] 1.1 Migration: `run_exits` (run id key, kind, args, `ack_seq`, facts, `exit_sha`), task `implementer`, `created_from_task`, verdict records. Verify: migration test.
- [ ] 1.2 Exit tools in `internal/factorytools` and handlers in `internal/core/mcp.go`; remove draft submit/accept/reject from the agent list; owner review maps to verdict. Verify: MCP tests per exit for the resulting status, assignee, triggers and parks.
- [ ] 1.3 410 for non-current runs, 409 rules, idempotent retry by run id. Verify: tests for exit after revoke (410), unseen directive (409 then success after display), `done` with an unseen non-authority trigger (409), duplicate exit returns the stored result.
- [ ] 1.4 `ack_seq` tracking in the adapter (`internal/factorytools/stdio.go`). Verify: adapter test where a 409 body is dropped before reaching the runtime does not raise `ack_seq`.
- [ ] 1.5 Exit facts in `cmd/factory-mcp`; approval bound to `{pr_number, approved_head}`; `done` requires `pr.state == merged` unless `abandon_branch`; `review_after_conflicts: required` refuses `done` after an unapproved head change. Verify: test with a temp git repo and a fake `gh` returning open/merged, a squash merge (accepted) and a head change under `required` (refused).
- [ ] 1.6 Block validation (open targets, no cycle, children) with fallback to ask. Verify: cycle and closed-target cases become `ask`.

## 2. Exit guard
- [ ] 2.1 Codex follow-up turn guard with `exit_guard_retries`. Verify: fake-RPC test where the first turn ends without an exit and the second calls one.
- [ ] 2.2 `no_exit` outcome, escalation to the manager (interim: role `manager`, else owner) with retry/reassign/accept/cancel; `accept` sets `handled_seq = wake_seq`. Verify: tests per answer; a company without a manager routes to the owner.
- [ ] 2.3 Adapter delivery: "queued — stop working" when Core is unreachable, retries with the same key, 410 ends retries; undelivered at process end → `lost`; retried exit refused with 409 → `no_exit`. Verify: adapter tests with Core down, Core back with 409, Core back with 410.

## 3. Authority input
- [ ] 3.1 `internal/core/authority.go` with interim role-based managers and delegating assignee. Verify: unit tests for owner, manager, delegator and a non-authority agent.
- [ ] 3.2 `factory_task_direct` and owner comment as authority triggers; `artifact_approved` for required/pinned artifacts. Verify: trigger rows with `authority = true`.
- [ ] 3.3 `authority_input` in every MCP response of the task's run, with rank. Verify: MCP test shows two directives with the higher-rank one marked as winning.
- [ ] 3.4 Park override in the trigger transaction (wait, ask, block, children, escalations except `inspect`). Verify: tests per park kind.
- [ ] 3.5 Urgent stop: outbox `run.stop` on `factory.<company>.nodes.<node_id>.control` (fan-out, outside the work-queue stream), worker subscription, heartbeat fallback, `revoked`, resume with the directive first; urgent artifact approval set by the approver. Verify: stop arrives within one second while 10 run commands are pending in the command stream; within one heartbeat with the control subject disabled.
- [ ] 3.7 Agent environment allowlist without owner/user tokens or CLI config; `factory` CLI denied on the agent PATH (shim in `node-agent`; worker env check now). Verify: worker test inspects the runtime env; an agent call to `factory` is refused.
- [ ] 3.6 Non-authority comments add no trigger and appear in the next brief. Verify: test.

## 4. Agent guidance and docs
- [ ] 4.1 Update `prompts/*`, the worker instruction block and skills: always end with one exit; read `authority_input`; merge before `done`. Verify: real Codex smoke `scripts/mcp-smoke.py` passes with explicit exits.
- [ ] 4.2 Update `docs/factory-mcp.md`, `docs/task-lifecycle.md`, `docs/messaging.md`. Verify: docs list the exits and 409/410 rules.
