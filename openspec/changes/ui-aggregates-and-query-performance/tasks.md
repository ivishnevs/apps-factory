Targets are measured on the benchmark fixture (section 0) with a 5 ms injected per-query delay unless a task says
prod. "Open tasks" includes conversation tasks; the fixture has 3 nodes, 10% queued runs, dependencies, parks and
duplicate warnings.

## 0. Measure first

- [ ] 0.1 Query counter on `Tx` (queries, total time) logged per view request with the route; `slow_view` logged above 1 s
- [ ] 0.2 Pool metrics (`sql.DBStats`: in use, wait count, wait duration) on `/health` (owner) and in the logs every minute
- [ ] 0.3 Enable `pg_stat_statements` in prod; record the top 20 statements by total time as the baseline in this change
- [ ] 0.4 Benchmark fixture builder (N = 10, 100, 500 open tasks; 3 nodes) and `BenchmarkViewSnapshots`, `BenchmarkStatus`, `BenchmarkTasks`, `BenchmarkThroughput`; baseline query counts recorded here
- [ ] 0.5 Prod baseline: p50/p95 of `/status`, `/tasks`, `/throughput`, `/api/overview`, `/api/tasks/{id}` over one hour (today 4.4–5 s, 4.4–5 s, 4.4–5 s, ~15–24 s)

## 1. Quick wins (no API change for the web)

- [ ] 1.1 `GET /api/tasks/{id}` no longer computes an Overview: children come from Core (`GET /tasks?parent=ID` and `?origin=ID` on the `tasks_parent`/`tasks_origin` indexes); target: `/api/tasks/{id}` makes ≤ 9 Core calls and no snapshot pass of the company; p95 < 500 ms in prod
- [ ] 1.2 `TaskDetail` reads the last 500 task events backwards (`before=0`, like `feedTail`) instead of paging the whole feed from the start; target: ≤ 5 feed queries for any task
- [ ] 1.3 `GET /api/tasks/{id}/materials` stops recomputing `TaskDetail` (it needs the task and its project only); target: ≤ 3 Core calls
- [ ] 1.4 Core computes one snapshot pass per company per moment: single flight plus a 2 s memo of `ViewSnapshots(company, "")` with verdicts and queued reasons, shared by `/status`, `/tasks`, `/throughput`, `/projects/{id}/why` (project filter applied in Go); target: one Overview = 1 pass, not 3
- [ ] 1.5 `queuedWhy` computed once per queued run per pass and reused by `/status` (`QueuedWhyAll`), conversation state and `bottlenecks`
- [ ] 1.6 `HTTPClient.Overview` runs its independent Core calls concurrently (bounded, e.g. 4) and the BFF single-flights it per company/session with a 2 s memo; target: `/api/overview` p95 < 2 s in prod with the old loader
- [ ] 1.7 View concurrency limit in Core: at most 6 concurrent snapshot passes per process (semaphore); a waiting view times out with 503 rather than taking a connection; target: in a load test with 10 tabs the claim and node polls never wait on the pool (`WaitCount` unchanged by views)
- [ ] 1.8 Web: hover/focus prefetch only when the cached task is older than 30 s; neighbours prefetched once per open, not on every poll
- [ ] 1.9 Hot predicates rewritten to match the partial indexes: `status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING')` (with `OR unreleased=1` where the writer slot needs it) instead of `status!='ENDED'`; `runs.project` instead of `spec->>'project'`; `EXPLAIN` before/after attached; target: no sequential or company-wide `runs` scan in the snapshot queries at 10,000 ended runs
- [ ] 1.10 Prod check after deploy: `/status`, `/tasks`, `/throughput` p95 < 1.5 s; no `slow_company_lock` from views; pool wait duration near 0 with 3 tabs open

## 2. Batched snapshot loader

- [ ] 2.1 Set-based loader for a set of tasks: tasks with wake/handled seqs, current runs, work ties, active parks with recipients, pending triggers, task pauses, ask parks, recovery escalations, required-artifact approvals, duplicate warnings, dependencies with person labels and root awaits, children of `children` parks, per-assignee live runs and counts; company facts (pauses, quota pauses, projects, writer runs, setup waits) once
- [ ] 2.2 Pass-wide node table (online, pause, update required, over plan, disk, drivers, labels, readiness per project, load) built once; `queuedNodes` and `nodesTx` read it; `nodeOverPlan` once per account per pass
- [ ] 2.3 `presentTask` folded into the loader (task pause, ask park and artifact approvals read once, not twice)
- [ ] 2.4 The claim and dispatch use the same loader with a set of one under the company lock
- [ ] 2.5 Property test: for generated fixtures (work and conversation tasks, people assignees, dependencies, parks, queued runs, ties, pauses, several nodes and projects, one and several companies) the batched snapshots equal the per-task snapshots, and the verdicts are identical
- [ ] 2.6 Targets: queries per pass ≤ 40 and constant in N (10/100/500); `ViewSnapshots` for 100 open tasks < 250 ms with 5 ms per query; 500 open tasks < 600 ms
- [ ] 2.7 Migration: `runs (company_id, agent, created_at) WHERE status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING')`; `runs.conversation` (backfilled, written at insert) replacing `spec->>'conversation'`; inbox partial index on open messages; each with `EXPLAIN (ANALYZE, BUFFERS)` before/after on the fixture
- [ ] 2.8 Bounded reads: `/approvals?open=1` (open only, comment state in one query), `/tasks?open=1&done_limit=` for the board; old unbounded forms kept for compatibility
- [ ] 2.9 Prod check: `/status`, `/tasks`, `/throughput` p95 < 300 ms; pass p95 < 250 ms; top `pg_stat_statements` compared with 0.3

## 3. Screen views, versions and 304

- [ ] 3.1 Company view version bumped in `Tx.Commit` of deciding transactions and on view-relevant `s.DB` writes (node online/offline and readiness changes; not every `last_seen`); audit list of direct writes recorded here
- [ ] 3.2 View cache per (company, view, parameters) with the version, the next time-based deadline and a 30 s ceiling; single flight; LRU bound per process
- [ ] 3.3 `GET /views/shell`: counts and server time without a snapshot pass; target p95 < 50 ms
- [ ] 3.4 `GET /views/board?project=`: open tasks with verdict, column, run, queued reason, duplicates; counts; bottlenecks; recent done (paged); target p95 < 300 ms for 100 open tasks and < 600 ms for 500 (uncached)
- [ ] 3.5 `GET /views/tasks/{id}`: task, verdict, queued reason, parks, attempts, comments, costs, exits, children, last 500 events, materials summary; target p95 < 200 ms
- [ ] 3.6 `GET /views/inbox`, `/views/chat`, `/views/nodes`; target p95 < 200 ms each
- [ ] 3.7 `ETag` on every view; `If-None-Match` answered with 304 without a pass; target: 304 p95 < 30 ms and 0 snapshot queries
- [ ] 3.8 Board column rule in Go (port of `columnOf`) with fixture tests shared with the web rule for one release
- [ ] 3.9 BFF passes `/api/views/*` through with the conditional headers; the web reads the views (shell, board, task page, inbox, chat, nodes) and sends `If-None-Match`; `useOverview` removed from the shell
- [ ] 3.10 `/status`, `/tasks`, `/throughput` served from the cache; `HTTPClient.Overview` kept for the TUI one release
- [ ] 3.11 Tests: version bump on every mutation kind (task, run, park, message, approval, node, pause, catalog); time-based deadline expiry refreshes a view; two companies never share a cached view; a non-member is refused before a cached view is served
- [ ] 3.12 Prod check: with 3 tabs idle on the board, ≥ 90% of polls are 304; DB queries from views < 5 per second

## 4. Push

- [ ] 4.1 `GET /views/stream` (SSE) sends `{view, version}` on a version change, coalesced to ≤ 1 event per second per company; keepalive 15 s
- [ ] 4.2 Core's feed SSE waits on the notifier instead of querying every second
- [ ] 4.3 Web subscribes once per tab, refetches the shown views on an event, falls back to 30 s polling when the stream is down
- [ ] 4.4 Target: a change is on the board within 1.5 s (p95) of its commit; an idle tab makes no view requests besides the stream

## 5. Multi-replica readiness, docs

- [ ] 5.1 The version source behind an interface; a `companies.view_seq` (or `LISTEN/NOTIFY`) implementation with a test running two Core processes on one database; listed in `saas-release-readiness`
- [ ] 5.2 TUI moves to the views; `HTTPClient.Overview` removed
- [ ] 5.3 Docs (`docs/` owner web performance notes, the views API) and spec deltas (`owner-web`, `owner-ui`, `task-engine`, `postgres-tenancy`, `event-feed`)
- [ ] 5.4 Benchmark query-count assertions in CI; timings published as informational
