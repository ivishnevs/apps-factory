## Why
Agents have no way to wait for the outside world (a build being processed, an external review, a deployment coming up) other than holding a run open or asking the owner to come back later. The owner sees projects through per-project feeds, run lists and an inbox, without one live picture of who is doing what, what needs attention, and what it costs. Risks this change removes: agents sit in runs or ping the owner while waiting for external processing; owners need a single place to see pending questions; and cancelled or timed-out runs report no cost, so waste stays invisible.

## What Changes
- **Timer-only waits**: `factory_exit_wait {reason, wake_at, deadline}` parks the task and schedules a delayed `wait_ended` trigger in the database — no probes, scripts or worker jobs. A deadline is mandatory, the interval has a minimum (default 15 min), consecutive waits without progress are limited, and exhaustion or the deadline escalates to the manager. Authority input wakes a waiting task early.
- **One event feed** (table and outbox delivery from `trigger-log-scheduling`) completed: Core's automatic events (handoffs, completions, questions, directives, escalations, failures and retries, node health, quota pauses) and agents' significant reports, with attention rules and filtered consumers. Managers see their subtree, the owner everything. A reporting policy defines mandatory reports, never blocking.
- **Telegram** for attention-needed items only (a question or escalation reached the owner, a node in trouble) plus a daily digest; replies answer questions, `/status` and `/stop` commands, free messages go to the default direct report's conversation (or `@name`).
- **Cost ledger**: one row per run with tokens and cost (reported by the runtime or computed from a price table), including stopped and failed runs.
- **Owner CLI**: `watch`, `status`, `log`, `show`, `answer`, `comment`, task actions (run, pause, resume, retry, reassign, cancel, reopen, reset-workspace), pause scopes (project, agent, quota key, node), `stop`/`start` (company kill switch), `costs`, `pending`; `--json` for scripts.

## Out of scope
Condition probes for waits (only if timer wakes prove costly), money budgets, a web dashboard (the owner API is the one a dashboard will use), desktop notifications.

## Dependencies
`trigger-log-scheduling`, `reliable-recovery`, `explicit-exits-authority` (wait exit, authority input), `hierarchy` (routing, subtree visibility, conversation tasks), `node-agent` (health), `runtime-drivers` (usage/cost events).

## Capabilities
### New Capabilities
- `timer-waits`: Timer-only waits with deadline, minimum interval, repeat limit and escalation.
- `event-feed`: One feed of Core events and agent reports with filtered delivery and Telegram attention/digest.
- `cost-ledger`: Per-run token and cost records and cost queries.
- `owner-cli`: The owner's console interface over the owner API.

### Modified Capabilities
None (generalizes the project feed and reports from the reports-decisions change).

## Impact
`internal/storage` (wait parks, timers, feed kinds and attention, `run_costs`), `internal/core` (sweeper timers, feed writes in transactions, SSE stream, digest), new `internal/notify/telegram`, `internal/bus` (feed subjects), `internal/factorytools` (`factory_exit_wait`, `factory_feed_list`), `internal/config` (`prices.yaml`, wait/notify policy fields), `cmd/factory` (new commands), docs `reports-decisions.md`, `messaging.md`, README.
