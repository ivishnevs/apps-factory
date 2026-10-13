## 1. Waits
- [ ] 1.1 `factory_exit_wait` with minimum interval, mandatory deadline, 14-day cap, episodes and `max_waits` policy fields. Verify: MCP tests for each refusal.
- [ ] 1.2 Sweeper timers: clear the park and add `wait_ended` at `wake_at`; fire once after a Core gap. Verify: fake-clock tests including a 3-hour gap.
- [ ] 1.3 `wait_deadline` / `wait_exhausted` escalations to the manager; authority input wakes early. Verify: tests for deadline, exhaustion and an early directive.

## 2. Event feed
- [ ] 2.1 Remaining feed kinds in cause transactions (exits, authority actions, escalations, outcomes, owner questions, approvals, quota pauses, reports) on the `feed_events` table from `trigger-log-scheduling`; complete `attention` rules incl. node restart loop, failed check, stalled heartbeat, `quota_paused`. Verify: per-cause tests; attention table test per kind. Partial: first attention rules — approval requests, questions to the owner, escalated messages, reports needing owner action, `ask` parks answered by the owner, `inspect` parks, OpenSpec blocks (05a94de); remaining kinds and rules pending.
- [ ] 2.2 `GET /feed/stream` (SSE) with filters over the existing feed subjects. Verify: a client reconnecting with the last `seq` receives every later row once.
- [ ] 2.3 Subtree visibility for managers (`factory_feed_list`, briefs). Verify: a lead cannot list a sibling team's events.
- [ ] 2.4 Reporting policy (`type: reporting`): mandatory reports, never blocking; missing report → manager event + brief reminder. Verify: `done` without the report is accepted and yields the event.

## 3. Telegram
- [ ] 3.1 `internal/notify/telegram` with chat binding, attention messages with option buttons, reminders, daily digest. Verify: tests with a fake Bot API server.
- [ ] 3.2 Inbound answers with `update_id` dedupe; `/status`, `/stop` (with confirmation); free messages to the default direct report or `@name`. Verify: duplicate update applied once; `@name` routing; `/stop` without confirmation does nothing.

## 4. Costs
- [ ] 4.1 `run_costs` one row per ended run from the usage stored on the run; backfill of earlier runs; `prices.yaml`; `cost_source`. Verify: revoked and lost runs get rows; backfill test; price-table computation test.
- [ ] 4.2 Cost queries by task, agent, day, model; closed-task totals. Verify: API tests.

## 5. Owner CLI
- [ ] 5.1 `watch`, `status`, `log`, `show` with `--json`. Verify: golden-output tests on a seeded database. Partial: `status`, `watch`, `log` (05a94de), `show` and `--json` on all four (8e2a367), tested against an in-process Core; golden-output tests on a seeded database pending.
- [ ] 5.2 `answer`, `comment [--urgent]`, task actions, pause scopes (project, agent, quota key, node), `stop`/`start`, `costs`, `pending` (`GET /pending/count`). Verify: CLI tests against the owner API; kill switch revokes all live runs and pauses claims; a node pause stops claims on that node only.
- [ ] 5.3 Update README, `docs/reports-decisions.md`, `docs/messaging.md`. Verify: docs list the commands and Telegram behaviour.
