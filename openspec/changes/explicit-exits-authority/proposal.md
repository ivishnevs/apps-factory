## Why
A completed turn is currently an implicit submission to IN_REVIEW, so a run that simply stops talking looks like finished work, and the factory cannot tell "done", "handed over", "waiting for an answer" and "gave up" apart. Owner messages arrive as separate conversation runs after the current work ends, so a running executor never learns that its manager or the owner changed the plan. Risks this change removes: a run that ends without any status change or handoff (a task that silently stalls), tasks marked done with their PR still open, and every agent comment waking the assignee so that many runs do nothing useful.

## What Changes
- **Explicit exits** as MCP tools: `factory_exit_handoff`, `factory_exit_submit`, `factory_exit_done`, `factory_exit_ask`, `factory_exit_block`, `factory_exit_wait` (timer semantics in `waits-events-cost-cli`). A run has at most one accepted exit; it fixes the run outcome `succeeded` and sets `handled_seq = ack_seq`.
- **Exit checks**: an exit is accepted only from the task's current live run (otherwise 410); it is refused with 409 and no state change while there is unseen authority input (`max_authority_seq > ack_seq`), and `done` also while there is any unseen trigger. The MCP adapter tracks `ack_seq` as the highest trigger seq it actually returned to the runtime.
- **Exit facts**: the MCP adapter attaches git facts (head, dirty, branch, ahead of default, pull request state) to every exit; `done` on a task with a branch is accepted only when its pull request is merged.
- **Driver exit guard**: the runtime is asked to call an exit before it may end; a turn that still ends without one is `no_exit` — a failed attempt that goes to the manager flagged "agent did not finish" (retry, reassign, accept as is, cancel). Exits queued while Core is unreachable are retried by the adapter; undelivered → `lost`, refused after new input → `no_exit`.
- **Authority input**: owner, managers up the chain and the delegating assignee can steer a task (`factory_task_direct`, owner comment, answers, new approved artifact versions). It appears in every MCP response, blocks exits until seen, wakes parked tasks, and when urgent stops the run over a per-node NATS control subject (heartbeat as the guarantee) and resumes the same session with the directive first. Comments from agents without authority never interrupt.
- **Agents act only through MCP**: no owner token or owner CLI in the agent environment.

## Out of scope
The hierarchy itself (`hierarchy` replaces the interim authority set defined here), worktrees, reset and seal (`task-worktrees`), Claude Code's Stop hook implementation (`runtime-drivers`), timer waits (`waits-events-cost-cli`).

## Dependencies
`trigger-log-scheduling` (triggers, parks, claim brief), `reliable-recovery` (outcomes, counters, escalations).

## Capabilities
### New Capabilities
- `run-exits`: Explicit exit tools, exit checks, exit facts, exit guard and `no_exit` handling.
- `authority-input`: Who may steer a task, delivery of their input to running and parked work, urgent stop and non-interrupting comments.

### Modified Capabilities
None (replaces implicit submission and draft submit from the task-engine and factory-mcp changes).

## Impact
`internal/factorytools` (exit and direct tools, `ack_seq` tracking in `stdio.go`), `cmd/factory-mcp` (git facts), `internal/core/mcp.go` (exit checks, `authority_input` in responses), `internal/core` (new `authority.go`), `internal/storage` (exits table, park override), `internal/worker` (Codex exit guard, stop subscription), `workers/claude` (stop subscription and environment allowlist until `runtime-drivers`; its Stop-hook exit guard is defined there), `internal/bus` (stop subject), `prompts/*`, `skills/`, docs `factory-mcp.md`, `task-lifecycle.md`, `messaging.md`.
