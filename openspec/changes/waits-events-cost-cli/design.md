## Context
After the earlier changes: triggers and parks drive scheduling; exits include a reserved `wait`; authority input overrides interruptible parks; escalations route up the hierarchy; drivers report usage/cost stored on the run; nodes report health; `feed_events` with outbox delivery exists since `trigger-log-scheduling` and every change writes its events into it. `factory_report_create` stores significant reports.

## Goals / Non-Goals
Goals: waiting costs no tokens and never strands work; the owner is interrupted only when needed and can see everything on demand; every run has a cost row.
Non-goals: probes, billing, budgets in money, a web UI.

## Decisions

### D1. Timer-only waits
`factory_exit_wait {reason, wake_at | after, deadline}`:
- `wake_at − now ≥ min_wait_interval` (policy, default 15 min, allowed 15–30) else refused with a hint;
- `deadline` is required, `wake_at ≤ deadline ≤ now + 14 days`;
- consecutive waits of a task without progress form an **episode**; a new wait cannot extend past the episode's first deadline; the episode count is limited by `max_waits` (policy, default 12).
Effect: a `wait` park `{reason, wake_at, deadline, episode, count}`. The Core sweeper (same tick loop, injectable clock) clears the park at `wake_at` and adds a `wait_ended` trigger; the agent checks the outside world itself in the next run and may wait again. Reaching the deadline or `max_waits` creates a `wait_deadline` / `wait_exhausted` escalation to the assignee's manager (answers: extend, retry, reassign, cancel). Authority input overrides the park early (`explicit-exits-authority` D7). A timer due while Core was not running fires once on the next tick. Wait wakes are ordinary runs and count against the daily budget, which bounds wait loops. Probes are added only if timer wakes prove costly.

### D2. One event feed
The feed (`feed_events`, `trigger-log-scheduling` D10) is complete after this change: exits (handoff, submitted, done, asked, blocked, waiting), directives and other authority actions, escalations opened/answered/forwarded, run outcomes (failed with "retry in N min", lost, timed out, no exit), questions reaching the owner, approvals, node health, quota pauses, and agent reports (`factory_report_create` writes a report row and a feed row), all written **in the transaction of the cause**. `attention = true` for: a question or escalation routed to the owner, `node.offline` longer than 10 min, `node.low_disk`, `node.disk_critical`, `node.restart_loop`, `node.check_failed`, `node.heartbeat_stalled`, `driver_unsupported`, `quota_paused`, a no-progress alert (`reliable-recovery` D8), a report with `needs_owner_action`.

**Reporting policy.** Policy rules `type: reporting` define mandatory reports per role or agent (e.g. a report on `submit` and `done`, a daily summary from managers) with required fields. They are never blocking: an exit is never refused for a missing report; the omission becomes a feed event for the agent's manager and a reminder in the agent's next brief.

### D3. Delivery and visibility
Each feed row also produces an outbox message on `factory.<company>.feed.<project>.<kind>`. Subscribers filter by subject: the Telegram notifier (attention only), the owner CLI `watch` through `GET /feed/stream` (server-sent events from Core, filtered), managers through `factory_feed_list` and their briefs (subtree only: tasks assigned within, or delegated by, their subtree), a future dashboard. Delivery is at least once with the feed `seq` as the deduplication key; the database remains the record.

### D4. Telegram
`internal/notify/telegram`, one bot per company, the owner's chat bound by a one-time code from the CLI. Outbound: attention items with the task, the question and option buttons; reminders for owner questions at 4 h and 24 h; a **daily digest** at a configured local time (done, in progress, waiting for the owner, failures and retries, nodes, cost of the day). Inbound: a button or a reply to a question message answers it (`update_id` deduplicated in the answer transaction); `/status` returns the `status` snapshot in short form; `/stop` asks for confirmation and then runs the kill switch; any other message is owner input to a direct report's conversation task — the default direct report, or the one addressed with `@name` (`hierarchy` D5). Retries with backoff on send failures; nothing else is sent to Telegram.

### D5. Cost ledger
`run_costs(company_id, run_id, task_id, agent, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, cost_source: reported|price_table|none, recorded_at)` — exactly one row per ended run, including revoked, failed, timed-out and lost runs, built from the usage stored on the run (`runtime-drivers`); a migration backfills rows for runs that ended before this change. When the runtime reports no cost, `cost_usd` is computed from `configs/<company>/prices.yaml` (model → prices per million tokens) with `cost_source = price_table`, else `none` with tokens only. Figures are API-equivalent for comparison and waste detection, not billing. Limits remain run counts (daily budget); money budgets are later.

A row is the run's own usage. Usage lines are cumulative for the run and replace each other per model (running usage, then the final usage). Runtimes report a resumed session cumulatively over its runs, so the driver subtracts the session's usage before the run: Codex the thread total before the turn; Claude the transcript's last `cost-state` entry, which Claude Code restores into `result.modelUsage` and `total_cost_usd` on resume. Found live on 2026-10-20 (tictactoe, company tvorog): 26 runs, ledger $14.32 with session snapshots stored per run (a developer session of three runs recorded $1.76 + $1.87 + $2.03 against a session total of $2.03); per-run deltas give $6.91, and the tokens equal the transcripts' deduplicated assistant usage exactly. The ledger, not a transcript recomputation, is authoritative: a recomputation at assumed list prices gave $7.96 because it prices Opus 5.5 differently from the SDK (Sonnet agrees to the cent).

### D6. Owner CLI
`cmd/factory`, user token, owner API only; plain text by default, `--json` for scripts:
- `watch` — live view refreshed every few seconds: each agent (working on T for 12 min / idle / paused / waiting, last step) and below it the feed ("T-50 developer → reviewer (submit)", "T-48 done", "T-51 waiting for your answer", "T-47 failed, retry in 5 min").
- `status` — one-shot snapshot plus nodes, pauses, quota keys, pending questions and escalations.
- `log [TASK] [--since 2h]` — feed history including triggers, runs, exits, parks and escalations.
- `show TASK` — description, comments, runs with outcome and cost, current reason and clearer, log tail.
- `answer ID <option|text>`, `comment TASK "…" [--urgent]` (owner comment = authority input).
- task actions: `run`, `pause`, `resume`, `retry`, `reassign`, `cancel [--cascade]`, `reopen`, `reset-workspace` — all with expected versions.
- pause scopes: `pause|resume --project P | --agent A | --quota-key K | --node N` (`pauses` rows; the node scope stops claims on that node).
- `stop` / `start` — kill switch at company scope: company pause, all live runs revoked, nodes stop claiming; seals complete. `start` lifts it; the name differs from task `resume` so the two are never confused.
- `GET /pending/count` (and `factory pending`) — number of questions and escalations waiting for the owner, for external counters and scripts.
- `costs [--task T | --agent A | --day | --model] [--since]`.

## Risks / Trade-offs
- Agents may wait in short loops → minimum interval, episode limit and the daily budget.
- Telegram noise → attention only plus one digest; everything else is in `watch`/`log`.
- Price tables go stale → `cost_source` shows how a figure was computed.

## Amendments

- 2026-10-20 (model label): a run's ledger model is the model with the most tokens, cache reads and writes included; counting only input and output named Claude Code's auxiliary Haiku calls. Migration 0003 relabels recorded rows (the cost does not depend on the label) (0355740).
