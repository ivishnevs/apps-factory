Status: proposed 2026-11-13, for the owner's decision.
Nothing is implemented. Decisions D1–D8 below need the owner's choice; each has a recommendation.

## Why

Seen in prod on 2026-11-13, a company with one active project, ~60 open tasks (conversation tasks included) and 3
nodes.

- **Core stopped answering for 24 minutes (11:43–12:07 UTC).** Requests queued and were cancelled while waiting to
  start a transaction: `Store.begin` (`internal/storage/storage.go:193`) takes the per-company advisory lock
  (`lockCompanyTx`, `internal/storage/db.go:132`) on a pool of 16 connections (`storage.go:169`). The cause was
  saturation by the owner web's polling, not one slow decision.
- **Fixed already (8f12782, a9d292e):** views read in a read-only repeatable-read transaction without the company lock
  (`Store.read`, `storage.go:211`): `ViewSnapshot(s)`, `QueuedWhy(All)`, `Bottlenecks`, `Escalations`, `TaskParks`,
  `ProjectContext`, the stall sweep and `PinnedUnavailable`. Lock holds over 1 s are logged as `slow_company_lock`
  (`db.go:60-71`). This stops views from queueing behind decisions, but each view still holds a pool connection for the
  whole pass, so the pool remains the shared bottleneck.
- **Still: `/status`, `/tasks` and `/throughput` take 4.4–5 s each in prod; `/api/overview` took ~24 s before the
  fix.** Each runs a full snapshot pass over the company's open tasks:
  - `taskSnapshotsTx` (`internal/storage/evaluate.go:77-103`) runs `snapshotTx` (`evaluate.go:174-325`),
    `conversationRunState` (`internal/storage/conversation_runs.go:39`) and `presentTask`
    (`internal/storage/present.go:42`) **per task**. For an agent's work task this is ~20 round trips: the current
    run, `wake_seq` (a second read of a row already loaded), the work tie twice (`evaluate.go:194` and
    `ties.go:19` run the same `workspaces` query), parks, triggers, three run lookups by agent
    (`evaluate.go:282,285,288`), the company's pauses (`pausedFor`), the company's quota pauses
    (`activeQuotaKeys`, `evaluate.go:304`, the same for every task), the task pause twice (`evaluate.go:308` and
    `present.go:54`), the project (`projectTx`, `evaluate.go:313`, the same for every task of the project), the writer
    run, the setup wait, the ask park, the recovery escalation, every required artifact's approval twice
    (`evaluate.go:272` and `present.go:84`), the duplicate warnings, and for every dependency `taskTx`,
    `personLabel` and `rootAwaits`.
  - A conversation task with a queued run adds `queuedWhy` (`internal/storage/queued.go:116`): 5 queries plus
    `queuedNodes` (`queued.go:258`), which per node reads the node pause, `nodeOverPlan` (account, subscription and
    own nodes: `internal/storage/billing.go:298`), the node readiness and the node load — ~6 per node, ~18 for 3 nodes.
  - `nodesTx` (`evaluate.go:106`) reads every node's load once per pass.
  - ~60 tasks × ~20 + queued runs × ~25 ≈ 1,300–1,500 sequential queries at 3–7 ms per round trip on Railway's
    network = the measured 4.4 s.
- **One overview is three such passes, and the web asks for overviews far more often than every 5 s.**
  - Every page polls `GET /api/overview` every 5 s: the shell reads it for the Inbox badge (`useInbox`,
    `web/src/components/app/shell.tsx:135` → `web/src/components/app/inbox/model.ts:89`), the query is
    `refetchInterval: 5000` (`web/src/lib/api/client.ts:55`).
  - The BFF (`internal/web/server.go:53-56`) calls `HTTPClient.Overview` (`internal/tui/client.go:405-448`), which
    makes **13 sequential Core calls plus one per live run**: `/health`, `/status`, `/tasks`, `/pauses`,
    `/pending/count`, `/nodes`, `/inbox`, `/alerts`, `/approvals`, `/throughput`, three `/costs`, and `/runs/{id}`
    for every live run (`client.go:434-440`).
  - `/status` (`internal/core/console.go:43-112`) computes `QueuedWhyAll` for queued runs (`console.go:72`), lists
    all tasks again (`console.go:84`), runs a snapshot pass per project (`console.go:91`) and the inbox per project
    (`console.go:104`). `/tasks` (`internal/core/tasks.go:222-264`) runs the same pass (`tasks.go:246`).
    `/throughput` (`tasks.go:140-155`) runs it again through `taskWaits` (`internal/core/stalls.go:333`) and then
    `queuedWhy` once more per queued run in `bottlenecks` (`internal/storage/throughput.go:331`). The same queued
    conversation run's reason is computed three times per overview.
  - `GET /api/tasks/{id}` (`server.go:57-65`) **computes a whole Overview** only to find the task's children in it
    (`client.go:453`, `childrenOf(id, all)`), then makes 7 more Core calls (`/tasks/{id}`, `/why`, `/attempts`,
    `/comments`, `/costs`, `/exits`, `/parks`) and reads the task's **entire feed from the start**, 100 events per
    page, keeping the last 500 (`client.go:469-482`). The task page polls it every 5 s (`client.ts:59`), the panel
    prefetches the previous and next task (`web/src/components/app/tasks/panel.tsx:41`) and every card prefetches on
    hover or focus (`web/src/components/app/tasks/parts.tsx:97`) — each prefetch is another full Overview.
  - `GET /api/tasks/{id}/materials` (`server.go:122-130`) computes the whole `TaskDetail` again (without the
    Overview) every 30 s (`client.ts:75`).
- **What that costs.** One overview ≈ 3 passes ≈ 13–15 s of one pool connection. A tab on a task page asks for two
  overviews per poll (the shell's and the task's) plus prefetches, i.e. ~6 passes per 5 s, which keeps ~5 of the 16
  connections busy continuously. Three such tabs (or one person and the TUI, which uses the same `Overview`) use the
  whole pool; the claim, the node polls and the exits then wait for a connection, a transaction holding the company
  lock can wait for a second connection, and everything else queues behind the lock.
- **Unbounded reads ride along.** `/tasks` returns every task ever, DONE and CANCELLED included
  (`internal/storage/tasks.go:386`); `/approvals` returns every approval ever, with one comment-state query per
  approval (`internal/storage/approvals.go:32-40`); `/costs?by=task` is the whole ledger; `/inbox` filters messages
  on `body->>'delegated_to'`, `body->>'decision_id'` and `body->>'report_id'` with no matching index
  (`internal/storage/messages.go:74-79`). The payload and the cost grow with the company's age, not with what the
  screen shows.
- **Hot predicates miss the indexes.** Run lookups by agent use `status!='ENDED'` (`evaluate.go:282,288,330`,
  `conversation_runs.go:21`) and the writer lookup uses `status!='ENDED' OR unreleased=1` (`evaluate.go:319`,
  `feed.go:176`). The only agent indexes are partial on `status IN ('CLAIMED','RUNNING','CANCELLING')`
  (`agent_slot`, `agent_live_runs`: `migrations/0001_baseline.sql:151`, `migrations/0004_agent_concurrency.sql:5`),
  and `workspace_writer` is partial on `status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING') OR unreleased=1`
  (`0001_baseline.sql:149`). Postgres cannot prove `status!='ENDED'` implies those predicates, so these queries fall
  back to `runs_company (company_id, created_at)` and filter every run the company ever had. `spec` and `body` are
  `json` (not `jsonb`), so `spec->>'project'` and `spec->>'conversation'` (`evaluate.go:329,337,353`) re-parse the
  whole spec — config snapshot included — for every candidate row, although `runs.project` is a column.
- **Polling elsewhere repeats work.** The chat page polls one `/api/messages` per (agent, project) pair every 5 s
  (`web/src/components/app/chat/use-chats.ts:68-76`); open topics (`client.ts:158`), agent runs (`client.ts:121`)
  and node keys (`web/src/components/app/nodes/enrollment.tsx:134`) every 5 s. Core's SSE (`serveSSE`,
  `internal/core/owner.go:214-252`) re-queries the feed every second per open stream.

The snapshot exists for the scheduler, which must decide on one locked, fresh state per task. The screens reuse it
task by task, three times per poll, through a BFF that composes 13 calls. The fix is a set of screen-shaped reads that
compute one pass per request, a snapshot loader whose query count does not grow with the number of tasks, and
polls that cost nothing when nothing changed.

## What changes

### 1. Inventory: what each screen needs and what it calls today

| Screen | Needs | Calls today (BFF → Core) | Poll |
|---|---|---|---|
| Shell (every page) | Inbox badge counts, company "now" | `/api/overview` → 13 + live-run calls, 3 snapshot passes | 5 s |
| Board (`tasks/page.tsx`) | open tasks per column with reason, clearer, wait, queued reason, run, node tie, duplicates; counts; bottlenecks; recent done | the same `/api/overview` (shared query key); columns computed in the browser (`columnOf`, `tasks/model.ts`) | 5 s |
| Task page / panel | task, verdict, queued reason, parks, attempts, comments, costs, exits, last 500 events, children, materials | `/api/tasks/{id}` → Overview + 7 calls + full feed paging; `/api/tasks/{id}/materials` → TaskDetail again + reports + artifacts; neighbours and hovered cards prefetched | 5 s; materials 30 s |
| Inbox | open questions, approvals, decisions, reports, escalations, alerts, topics | `/api/overview` (inbox, approvals, alerts, pending) + `/api/core/topics?open=1` | 5 s |
| Chat | per-chat messages, replying run, open items not in a chat | `/api/overview` (agents, status, tasks) + `/api/messages` per (agent, project) pair + topics + teams | 5 s each |
| Nodes | nodes with in-use, tied, readiness; live runs; agent runs | `/api/overview` (`/nodes`, live runs) + `/api/core/agents/runs` + `/api/core/node-keys` | 5 s each |
| Run page | run, events, tool calls | `/api/overview` + `/api/runs/{id}` + SSE | 5 s |

Background tabs do not poll (TanStack Query's `refetchIntervalInBackground` is off by default), so the load is per
visible tab, per person, plus the TUI.

### 2. Screen aggregates in Core

New read-only owner routes in Core, each computed in **one** read transaction (`Store.read`) with **one** snapshot
pass at most, and served by the BFF as they are (the BFF no longer composes):

- `GET /views/shell` — the counts the shell shows (pending/inbox counts, alerts count, awaiting-you count), the
  server time, and the company's view version. No snapshot pass: counts come from indexed queries.
- `GET /views/board?project=` — open tasks with their verdict (reason, clearer, wait, tie, awaits), the board
  column, the current run (status, node, started, cost so far) and its queued reason, duplicate warnings; per-column
  counts; bottlenecks; the last N done/cancelled tasks (a page, not all history); the nodes' names for labels. One
  snapshot pass feeds the verdicts, the bottlenecks (`taskWaits` from the same snapshots) and the queued reasons
  (`queuedWhy` computed once per queued run and shared).
- `GET /views/tasks/{id}` — what the task page shows: the task and verdict, queued reason, parks, attempts, comments,
  costs, exits, children (indexed `tasks_parent`/`tasks_origin` lookups, `0001_baseline.sql:76-77`), the last 500
  events read backwards (`feed_task`, `0001_baseline.sql:397`), and the materials summary. Single-task snapshot.
- `GET /views/inbox` — open items (questions, approvals still open, decisions, reports, escalations), alerts and
  open topics: only what is open, bounded.
- `GET /views/chat` — the owner's chats with their last message and unread marker, the replying run, open items
  not in a chat; messages of the open chat stay on `/messages?conversation=` (one poll, not one per pair).
- `GET /views/nodes` — nodes with in-use, tied, readiness, live runs and agent run counts in one read.

The existing routes (`/status`, `/tasks`, `/throughput`, `/tasks/{id}/why`, …) stay for the CLI, Factory MCP and
older clients and are reimplemented on the batched loader (section 3), so they get fast too. `HTTPClient.Overview`
keeps working for one release for the TUI, then moves to the views.

### 3. A snapshot loader whose query count does not grow with N

- **Set-based loading.** `taskSnapshotsTx` loads, for the company's (or project's) open tasks, in a fixed number of
  queries: tasks with `wake_seq`/`handled_seq` (one query, not one per task); their current runs; work ties
  (`workspaces` with `unpushed=1`); active parks with recipients; pending triggers (window per task); task pauses; ask
  parks and recovery escalations; required-artifact approvals; duplicate warnings (already set-based in
  `presentDuplicates`, `duplicates.go:440`, but called per task today); dependency tasks and their person labels;
  children for parks of kind `children`; and, per assignee (not per task), live work/conversation runs and counts.
  Company-wide facts are read once: pauses, quota pauses, projects (writer runs and setup waits per project), nodes
  with loads, faults and plan limits. Go assembles each `tasks.Snapshot` from maps.
- **Per-node work once per pass.** `queuedNodes` splits into a pass-wide node table (online, pause, update required,
  over plan, disk, drivers, labels, readiness per project, load) built once, and a per-run check against it.
  `nodeOverPlan` is computed for all nodes of the account in one call per pass.
- **One code path for views and decisions.** The claim (`internal/storage/claim.go:45`) and dispatch keep a locked
  single-task snapshot, implemented as the same loader with a set of one task, so a verdict on the board and the
  scheduler's verdict cannot drift apart. A property test compares the batched snapshot with the per-task one over
  generated fixtures.
- **Predicates that hit indexes.** Live-run lookups use the live statuses the unique indexes use
  (`status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING')`, with `OR unreleased=1` where the writer slot needs it)
  instead of `status!='ENDED'`; `runs.project` replaces `spec->>'project'`.
- **New indexes and columns** (one migration, built `CONCURRENTLY` where the table is large):
  - `runs (company_id, agent, created_at) WHERE status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING')` for the
    per-assignee run lookups.
  - `runs.conversation boolean` (from `spec->>'conversation'`, backfilled, written at insert) so the kind filter
    (`evaluate.go:353`) does not parse the spec.
  - `messages (company_id, created_at, seq) WHERE status IN ('ESCALATED','PENDING','UNREAD')` for the inbox, and the
    inbox's `body->>` conditions promoted to columns or kept as an index expression (D7).
  - `approvals (company_id, status)` exists (`0001_baseline.sql:285`); the view filters open approvals with it instead
    of reading all.
  - Each index is justified by an `EXPLAIN (ANALYZE, BUFFERS)` of the query on a fixture before and after, recorded
    in the task.
- **Measuring.** A query counter on `Tx` (it already wraps `QueryContext`/`QueryRowContext`/`ExecContext`,
  `db.go:107-115`) logs queries and duration per view request; `sql.DBStats` (wait count and wait duration of the
  pool) is exported on `/health` or metrics; `pg_stat_statements` is enabled in prod and its top statements are
  reviewed after each phase; `slow_company_lock` stays. A Go benchmark builds a fixture of N open tasks (N = 10,
  100, 500; conversation tasks, queued runs, dependencies, parks, 3 nodes) and asserts the query count of a pass is
  constant in N, with an optional injected per-query delay (5 ms) to reproduce the network.

### 4. Caching, versions and conditional polls

- **Single flight.** Concurrent requests for the same view of the same company share one computation (Core,
  `golang.org/x/sync/singleflight` or an equivalent map of in-flight calls).
- **Company view version.** Every committed deciding transaction (`Tx.Commit` of a transaction from `Store.begin`)
  bumps the company's view version; writes outside `begin` that change what a view shows (a node going on/offline,
  heartbeat state changes, not every `last_seen`) bump it too. A view's result is cached per (company, view,
  parameters) together with the version it was computed at and the earliest moment a time-based verdict changes
  (backoff end, pause `until`, pin timeout, session affinity release, budget day). It is reused while the version is
  the same and that moment has not passed, with a hard ceiling (e.g. 30 s).
- **ETag / 304.** Each view returns `ETag: "<version>-<deadline bucket>"`; a poll with `If-None-Match` gets `304 Not
  Modified` without a snapshot pass and without a body. The BFF forwards the header both ways; TanStack Query keeps
  the cached data on 304.
- **The owner's own actions** bump the version on commit, so the next poll is fresh; the web also invalidates its
  queries after an action as it does today (`client.ts:96`).

### 5. Push instead of polling (last phase)

- `GET /views/stream` (SSE): one stream per tab sends `{view, version}` when the company's view version changes
  (from the same in-process notifier as `Store.Wake`, `storage.go:67`, `core.go:113`), coalesced to at most one event
  per second. The tab refetches the views it shows (each a 304 or a fresh body). Polling stays as the fallback
  (30 s) when the stream is down. Core's SSE loop waits on the notifier instead of re-querying every second
  (`owner.go:251`).

### 6. Web changes

- The shell reads `/views/shell`; the board `/views/board`; the task page `/views/tasks/{id}`; inbox, chat and nodes
  their views. `useOverview` is removed from the shell. Prefetch of a task on hover reads `/views/tasks/{id}` (cheap
  after phase 2) and only when no fresh copy exists.
- Polls send `If-None-Match`; the intervals stay 5 s until SSE lands, then become the 30 s fallback.

## Decisions

### D1. Where screen aggregates are composed

- **A. In Core, as `/views/*` routes (recommended).** One read transaction and one snapshot pass per request; no
  13-call fan-out over the network; the TUI, Telegram and the web share them; Core can cache by version.
  Risk: Core grows screen-shaped routes; mitigated by keeping them read-only and built from the same store helpers.
- **B. In the BFF (`internal/web`), parallelising Core calls and caching there.** No Core API change, but each Core
  call still does its own pass, caching cannot see Core's commits (no version), and the TUI does not benefit.
- **C. Generic query API (GraphQL-like).** Flexible, but a large surface and the N+1 moves into resolvers.

### D2. Board columns: computed in Core or in the browser

- **A. Core returns each task's column (recommended).** One rule in Go (ported from `columnOf`,
  `web/src/components/app/tasks/model.ts`) for web, TUI and Telegram; the board view does not need live runs, status
  and nodes just to place cards. Risk: a port drifts from the web rule; mitigated by fixture tests of both for one
  release, then the web rule is removed.
- **B. The browser keeps `columnOf`; Core returns the flat list with what it needs.** Less change now; the payload
  keeps carrying the inputs of the rule.

### D3. Caching

- **A. No cache, only the batched loader.** Simplest; every poll still costs a pass (~20 queries, tens of ms).
- **B. Per-company TTL (2–5 s) with single flight.** Cheap to build; staleness up to the TTL after the owner's own
  action unless invalidated; does not give 304s.
- **C. Version-invalidated cache with time deadline and ETag (recommended, phase 3)**, with single flight and a short
  TTL as the phase 1 stopgap. Fresh after every commit, 304 when nothing changed. Risk: a write path that changes a
  view but does not bump the version shows stale data until the ceiling; mitigated by bumping in `Tx.Commit` (all
  deciding writes) plus an audit of `s.DB` writes, and the 30 s ceiling.

### D4. Source of the view version (and multi-replica Core)

- **A. In-process counter per company, bumped in `Tx.Commit` (recommended now).** No extra queries; correct while
  one Core process serves, which is the deployment today (node nonces already assume it,
  `docs/security-review-2026-11.md:54`). Risk: with several replicas, a commit on one is not seen by another's
  cache.
- **B. `companies.view_seq` column bumped in each deciding transaction.** Works across replicas; one extra `UPDATE`
  per deciding transaction (under the company lock, so no contention) and one cheap read per poll.
- **C. Postgres `LISTEN/NOTIFY` per company.** Push across replicas; needs a dedicated connection per replica and
  reconnect handling.
- Recommendation: A, with the version behind an interface so B (or C for SSE) replaces it when Core runs more than
  one replica; the multi-replica switch is listed in `saas-release-readiness`.

### D5. How the snapshot is batched

- **A. Set-based queries per fact, assembled in Go (recommended).** ~20–30 queries per pass regardless of N; the
  same loader serves a set of one for the claim; readable; testable against the per-task loader.
- **B. One SQL statement with CTEs/`LATERAL` producing snapshots as JSON.** Fewest round trips, but the verdict
  inputs move into SQL that is hard to read and test, and it diverges from the claim path.
- **C. Materialise each task's verdict on commit (a `task_views` table).** Views become a single indexed read, but
  every deciding transaction must recompute the verdicts it can affect (dependencies, nodes, pauses fan out); a
  missed recompute is a stale board. Keep as a later option if A is not enough.

### D6. Push or poll

- **A. Poll with 304 (phase 3), then SSE version events (phase 4) (recommended).** 304 polls already remove the
  passes; SSE removes the latency and the request rate.
- **B. SSE carrying full view payloads.** Fewer requests, but large repeated payloads and per-stream computation.
- **C. Keep plain 5 s polling.** Only acceptable with D3-C in place.

### D7. JSON columns in hot predicates

- **A. Promote the filtered fields to columns (recommended for the hot ones):** `runs.conversation` (from
  `spec->>'conversation'`), use the existing `runs.project`; `tasks.parent_task_id`/`origin_task` already have
  expression indexes and can stay; the inbox's `delegated_to`/`decision_id`/`report_id` as stored generated columns
  if `EXPLAIN` shows the inbox scan matters.
- **B. Convert `json` to `jsonb`.** Faster `->>` and GIN indexes, but a rewrite of large tables (`runs`, `messages`,
  `tasks`) and output key order changes.
- **C. Leave as is.** The `json` parse cost stays on every candidate row.

### D8. Old routes and the Overview

- **A. Keep `/status`, `/tasks`, `/throughput` and `HTTPClient.Overview` for one release on the new loader, then
  move the TUI to the views and drop the Overview (recommended).** No break for CLI, MCP and older nodes/clients.
- **B. Remove them with the web switch.** Less code, but breaks the TUI and scripts.

### Risks across decisions

- **Staleness.** Views read a snapshot without the lock; with a cache they may lag a commit by up to the TTL (phase
  1) or not at all (version, phase 3). Actions are re-checked by Core in their own locked transaction, so a stale
  view never causes a wrong decision, only a stale display.
- **Consistency with the scheduler.** The scheduler and claim keep locked snapshots; views use read-only repeatable
  read. Both go through the same loader (D5-A), so the rules are identical; the property test guards the batched
  path against the per-task path.
- **Multi-company.** All caches, versions and single flights are keyed by company; a company's pass never blocks
  another's; memory is bounded (LRU of idle companies). Views are company-wide today (no per-person filtering of
  tasks); the route still checks the caller's membership before serving a cached result, and if per-person views
  come, the key includes the scope.
- **Multi-replica Core.** See D4; until then the in-process version is correct for the single process.
- **Pool exhaustion remains possible** under enough load; phase 1 adds pool wait metrics and a separate, smaller
  limit for view requests (a semaphore of e.g. 6 concurrent view passes per Core) so views can never take the
  connections the claim, polls and exits need.
- **Older web/TUI builds** keep calling the old routes; they get the faster loader but no 304s.

## Impact

- Specs: `owner-web` (screens read views; polls are conditional; SSE refresh), `owner-ui` (TUI reads views after one
  release), `task-engine` (one snapshot loader for views and decisions; verdict unchanged), `postgres-tenancy`
  (indexes, view version, view concurrency limit), `event-feed` (SSE waits on the notifier).
- Core: new `internal/core/views.go` (routes, single flight, cache, ETag), `console.go`, `tasks.go`, `stalls.go`
  (`/status`, `/tasks`, `/throughput` on the loader), `owner.go` (`serveSSE` on the notifier), `core.go` (version
  notifier).
- Storage: `evaluate.go`, `queued.go`, `present.go`, `ties.go`, `conversation_runs.go`, `throughput.go`,
  `nodes.go`, `billing.go` (set-based loader, per-pass node table), `db.go` (query counter, version bump in
  `Commit`), `storage.go` (pool metrics), `messages.go`, `approvals.go` (bounded reads), a new migration (indexes,
  `runs.conversation`).
- BFF: `internal/web/server.go` (`/api/tasks/{id}` without Overview; `/api/views/*` passthrough with ETag),
  `internal/tui/client.go` (`TaskDetail` without the full feed walk and without `all`; parallel Overview until
  removed).
- Web: `web/src/lib/api/client.ts`, `components/app/shell.tsx`, `components/app/inbox/model.ts`,
  `components/app/tasks/{model.ts,panel.tsx,parts.tsx}`, `app/c/[company]/tasks/page.tsx`,
  `components/app/chat/use-chats.ts`, `app/c/[company]/nodes/page.tsx`.
- Ops: `pg_stat_statements` enabled on the prod database; a benchmark in CI (query count only; timings are
  informational).
- No change to what a verdict says, to the scheduler's decisions or to the node protocol.
