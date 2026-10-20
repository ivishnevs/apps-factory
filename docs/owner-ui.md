# Owner UI (`factory ui`)

A full-screen terminal UI for the owner: what every agent is doing, the tasks and why they wait, a live view
of a run, the flow of work between agents (who passes what to whom), the feed and the inbox — with the owner
actions in place. It is a client of Core's owner API (the same token as the CLI); it keeps no state.

```sh
export FACTORY_URL=http://127.0.0.1:8080 FACTORY_TOKEN=...   # as for the other factory commands
./bin/factory ui                      # live, against Core
./bin/factory ui --demo               # a built-in demo company, no Core needed
./bin/factory ui --record state.json  # snapshot Core's current state into a fixture file (0600)
./bin/factory ui --fixture state.json # browse a recorded fixture, no Core needed
```

In `--demo` and `--fixture` mode actions are not sent anywhere (the status bar says
`fixture: not sent — …`); the live run view replays the recorded run events. A recording holds the overview,
the last 2000 feed rows, the detail of every open task (and up to 50 closed ones) and the events and tool
calls of the live runs and the latest attempts; run leases are left out, the token is never written.

Built with Bubble Tea 2.0.9, Bubbles 2.2.1 and Lip Gloss 2.0.6 (`charm.land/...` v2 modules, Go 1.25+). Code:
`internal/tui` (the `Client` interface and its HTTP and fixture implementations are separate from the
views), `cmd/factory/ui.go`.

## Views

`1`–`6` or `tab` switch views, `?` shows the hotkeys, `q` quits. Every action asks for confirmation first
(`y`/`enter` sends, `n`/`esc` cancels); text actions ask for the text, then confirm.

### 1 Dashboard

Every catalog agent in its `reports_to` tree: idle, queued, running or paused; its live run and task (or,
faint, the open task it holds); today's cost (`GET /costs?by=agent&since=<local midnight>`); the age and text
of its last activity — for a running agent the last line of its live run (a `tail=3` run stream per live
run), otherwise its last feed row. Below: task counts by status, attention counters (`GET /pending/count`
and attention feed rows of the last 24 h), the node strip (`GET /nodes`: slots in use, load, failing
checks, offline age) and the pauses. `enter` on a running agent opens its live run, on another its tasks;
`t` its tasks, `f` its flow.

```
 1 Dashboard  2 Tasks  3 Run  4 Flow  5 Feed  6 Inbox(3)   ? help
Agents  7 agents · reports_to tree · enter: run/tasks · t tasks · f flow
AGENT               STATE    TASK    TODAY  AGE  LAST ACTIVITY
calorie-manager     idle     CAL-1   $0.12  1h   waits for the answer to m-ask-1
├ calorie-designer  paused   -       $0.55  3h   CAL-2 done
├ calorie-ios       running  CAL-3   $1.84  2m   exit guard refused the turn's …
└ calorie-qa        idle     CAL-4   -      4h   Test meal entry on iPhone SE
manager             idle     N-3     -      20m  factory_release_publish: relea…
├ demo              idle     N-2     $0.92  45m  N-2: 3 runs without progress
└ reviewer          running  N-1     $0.31  1m   Testing the export on a 10k-li…

Tasks     IN_PROGRESS 1 · IN_REVIEW 1 · READY 6 · BACKLOG 1 · DONE 1  (runnable…
Attention inbox 3 · approvals 1 · alerts 1 · inspect 0 · in review 1 · feed ! 6…
Nodes     ● mac-studio 2/4 load 1.2 · ○ mac-mini offline 7m fail:disk
Pauses    agent calorie-designer: waiting for the brand guide
```

### 2 Tasks

`GET /tasks` with the `why` reason of each task; moving tasks first. `/` filters with
`p:PROJECT s:STATUS a:AGENT ! open conv` and free words (id, title, reason); `!` toggles "waits on the
owner" (its clearer is the owner, or an inbox item is about it); conversation tasks show only with `conv`.
`enter` opens the detail: why and who clears it, pending triggers, active parks, dependencies, children
(parent or origin), the origin (who created it from which task and run), the body, every run with outcome,
exit and cost, comments and the recent events. At 140 columns and wider list and detail sit side by side.

Actions on the selected task: `p` pause · `u` resume · `r` retry · `n` run now · `x` cancel (with
`cascade` when it has open children) · `a` reassign · `P` reprioritize · `c` comment · `d` directive
(start the text with `!` for an urgent one, which stops the live run) · `L` live run · `f` flow.

```
 CAL-3  [IN_PROGRESS]  Build the meal entry screen
 project calorie · assignee calorie-ios · priority 7 · v3 · attempt 2
 origin: created by calorie-manager from task CAL-1 · 4h ago
 why: live_run — run r-ios-7 is running
   clears: calorie-ios — wait for the run
 triggers: directive by owner (authority)
 depends on: CAL-2 DONE

 Runs (2)
   #1 r-ios-6    failed     10-20 12:50  -           $0.61 (reported)
      xcodebuild: the simulator did not boot
   #2 r-ios-7    RUNNING    10-20 14:18  -           ~$1.23
```

### 3 Run

A run's activity as it happens: agent messages, tool calls (shell commands and `factory_*` MCP calls apart,
exits marked), command refusals and exit-guard refusals, Core's record of each Factory MCP call with its
status (a 4xx is a refusal), and the end. It streams `GET /runs/{id}/events/stream` and reloads the run and
its tool calls every 2 s. Follow mode keeps the newest line in view; scrolling up stops it, `G` or `f`
resumes. Without a run the view lists the live runs to pick from.

```
Run r-ios-7 calorie-ios · task CAL-3 · RUNNING · started 12m ago · ~$1.23
follow on · live · 15 events · j/k scroll · G bottom · t task · esc back
14:18:00 say     Resuming CAL-3. The owner asked for offline-first; I'll check …
14:19:00 mcp     factory_task_get {"task":"CAL-2"}
14:20:00 shell   $ xcodebuild -scheme Calorie -destination 'platform=iOS Simula…
14:21:00 shell   $ git push --force origin af/CAL-3
14:21:00 refusal command refused (deny, rule no-force-push): git push --force o…
14:27:00 say     All 42 tests pass. Submitting for review.
14:28:00 exit    factory_exit_submit {"reviewer":"calorie-manager","summary":"m…
14:28:00 refusal exit guard refused the turn's end: submit needs a pushed branc…
14:28:00 refusal core: factory_exit_submit refused 409
```

### 4 Flow — who passes what to whom

Built from the feed: task creation (`created_by` → assignee), exits (`submit`, `handoff`, review verdicts
`approved`/`changes_requested`, `done`, `asked`, `blocked`), messages (questions up the `reports_to` chain,
answers, escalations, forwards), directives, comments, reassignments, pauses, escalations and approval
requests. The timeline is filterable by project (`p`), agent (`a`) and task (`t`); `g` switches to the graph:
each edge from → to with its count, kinds and last time, next to the `reports_to` tree.

```
09:50 calorie-manager → calorie-desig… create    CAL-2  "Design the meal entry …
11:50 calorie-ios → calorie-manager    question  CAL-3  Should the portion pick…
12:00 calorie-manager → calorie-ios    answer    CAL-3  Grams only for v1.
12:30 demo → reviewer                  submit    N-1    markdown export with ro…
12:40 reviewer → demo                  changes   N-1    escape pipes in tables
13:20 owner → calorie-ios              directive CAL-3  Keep the entry screen o…
13:50 manager → demo                   handoff   N-3    rebase N-2 on main befo…
14:00 manager → owner                  escalate  N-3    N-3 waits on N-2
```

```
FROM                  TO                 N LAST   KINDS
manager          ──▶  demo               3 13:50  create×2 handoff
calorie-manager  ──▶  calorie-ios        2 12:00  answer create
demo             ──▶  reviewer           2 13:35  submit×2
manager          ──▶  owner              2 14:10  approval escalate
```

### 5 Feed

`GET /feed/stream` (all rows from the start, the last 3000 kept). Attention rows are red with `!`; `!` shows
only them, `p` cycles the project, `/` filters by words. Scrolling up pauses the view; `G` returns to the
newest.

### 6 Inbox

Questions, escalations and approvals waiting on the owner (`GET /inbox`, `GET /approvals`). A one-question
form shows its options: `←`/`→` choose, `enter` answers with the option (a structured answer); `a` answers in
free text. On an approval, `y` approves (reason prefilled) and `n` rejects (reason required).

### Status bar and kill switch

Core URL and reachability (stale data stays on screen while Core is unreachable, with its age), the company,
`▶ running` or `■ STOPPED` (any company pause, shown first so it is never cut off), the feed stream state and
the last action's result. `K` is the kill switch: stop with a reason (`POST /stop`), or start again
(`POST /pauses/resume` with scope company).

## Behaviour

- Refresh: the overview (`/health`, `/status`, `/tasks`, `/pauses`, `/pending/count`, `/nodes`, `/inbox`,
  `/approvals`, `/costs`) every 3 s, one read at a time; the agent catalog once. Only `/health` and `/status`
  failing marks Core unreachable; other parts keep their last value.
- Streams reconnect after an error with `Last-Event-ID` (1 s backoff doubling to 15 s) and deliver each row
  once; a 400/401/404 is not retried.
- The token is sent only in the `Authorization` header; it is not part of a URL, an error or the screen.
- Layout: every frame is exactly the terminal's size (no shifting lines, so no flicker) and follows resizes;
  80×24 is the smallest designed size. Colors are the 16 ANSI colors, which the terminal's theme maps, so they
  read on dark and light backgrounds; selection is reverse video.

## Core API used and added

Added for the UI: `GET /runs/{id}/events/stream` — a run's events as server-sent events (`id` = the event
sequence; resume with `Last-Event-ID` or `?after=`; `?tail=N` starts at the last N events; the worker lease is
left out; after an ended run's last event `event: end` with `{"status","outcome"}` closes the stream). It
shares its loop with `GET /feed/stream`. Message feed rows now carry `sender → recipient (type): body` as
their summary, which the flow and the feed show.

## Tests

`go test ./internal/tui`: the HTTP client against an `httptest` fake Core (overview, partial failures, task
detail, every action's route and body, stream reconnect with `Last-Event-ID`, run stream end, 404 give-up,
SSE parsing, recording without secrets), model updates (confirmations, inputs, inbox answers and approvals,
kill switch, run follow mode, feed dedup and scroll, resize, quit) and golden screens of every view at fixed
sizes (`internal/tui/testdata/*.golden`; `go test ./internal/tui -update` rewrites them). The Core endpoint
is tested in `internal/core/streams_test.go` (Postgres).
