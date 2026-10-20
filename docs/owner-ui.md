# Owner UI (`factory ui`)

A full-screen terminal UI for the owner: what every agent is doing right now, the work as a Kanban board,
a live transcript of any run, who passes what to whom, the feed and the inbox — with every owner action in
place. It is a client of Core's owner API (the same token as the CLI) and keeps no state.

```sh
export FACTORY_URL=http://127.0.0.1:8080 FACTORY_TOKEN=...   # as for the other factory commands
./bin/factory ui                      # live, against Core
./bin/factory ui --demo               # a built-in demo company, no Core needed
./bin/factory ui --record state.json  # snapshot Core's current state into a fixture file (0600)
./bin/factory ui --fixture state.json # browse a recorded fixture, no Core needed
```

In `--demo` and `--fixture` mode actions are not sent anywhere (the footer says `fixture: not sent — …`);
run views replay the recorded events. A recording holds the overview, the last 2000 feed rows, the detail
of every open task (and up to 50 closed ones), and the events and tool calls of the live runs and latest
attempts; run leases are left out and the token is never written.

Built with Bubble Tea 2.0.9, Bubbles 2.2.1 (textinput, viewport, spinner) and Lip Gloss 2.0.6
(`charm.land/...` v2 modules, Go 1.25+). Code: `internal/tui` — the `Client` interface with its HTTP and
fixture implementations is separate from the views — and `cmd/factory/ui.go`.

## Layout

- **Header**: the app name, one chip per project — `[ ● calorie 22% ]`: the dot is green while the project
  has live runs, the percentage is its done share; the selected chip (`[` / `]`, or a click) narrows every
  screen to that project — and a live clock.
- **Tabs**: `1 Dashboard  2 Board  3 Flow  4 Feed  5 Inbox`, then Core's connection dot, the company,
  `▶ running` or `■ STOPPED …` (the kill switch, never cut off) and the count of what waits on you (`⚠ 4`).
- **Prompt bar** (amber): the oldest item waiting on you, answerable from any screen — `Y`/`N` approve or
  reject an approval (with a reason), `A` answers a question (a chooser of its options, or free text).
- **Footer**: the keys of the current screen, or the result of the last action.
- **Toasts**: a fresh attention event (approval request, escalation, alert, node trouble) slides in at the
  top right for a few seconds.
- **Command palette** (`ctrl+k` or `:`): fuzzy search over tasks, agents, live runs, projects and actions
  (go to a screen, stop/start the company, answer the waiting item, show what needs you).
- **Mouse**: click tabs, project chips, panels, rows and cards (a second click on a card opens it); the wheel
  scrolls.

Every action asks first (`y`/`enter` sends, `n`/`esc` cancels); text actions ask for the text, then
confirm. `?` lists all keys.

## 1 Dashboard (home)

Five panels with thin borders and centered titles; `h`/`l` (or a click) moves the focus ring, `j`/`k` moves
inside a panel, `z` zooms the focused panel to the full screen.

- **Agent Tree** — the `reports_to` tree under you. Each agent: a status dot (pulsing `●` running, `◐`
  waits on someone — you first —, `⏸` paused, `○` idle), its name in its own stable color, what it is doing
  (the live run's task, what it waits on, or the next task) and a timer (run time, or how long it waits).
  A running agent has a sub-row with its current step (`▸ swift test …`, `✎ Edit: …`, `⇄ factory_…`) and
  that step's age. `enter` on a running agent opens its run, `f` its flow.
- **Tasks** — the selected agent's tasks as a checklist (`[→]` running `← running`, `[!]` waits on you,
  `[◎]` in review, `[✓]` done, `[ ]` pending), each waiting task with a dim `↳ blocked by: …` or
  `↳ … waits on …` line. On the owner row: everything that waits on you or is in review. `enter` opens
  the task, and the task actions work here too.
- **Stats** — the selected agent's live run: model, a context gauge (input and cache-read tokens of the
  context window, green→red), tokens in/out, cache read/write, run cost and duration; today's cost and runs,
  and sparklines of its spend over 24 h and its activity. On the owner row: company totals.
- **Activity** — one live stream of every agent's steps (from each live run's event stream) and the feed:
  `HH:MM:SS [agent] Tool  argument`, each agent in its color, refusals red, exits green, attention rows
  marked `⚠`; the newest line has a blinking cursor.
- **Pulse** — a mini-map of the board (a bar per column), the company's spend over 24 h, the node strip and
  an activity sparkline per agent.

Below 110 columns the dashboard shows Agent Tree, Tasks and Activity; at 80×24 it still fits.

```
 ◆ APP FACTORY  [ ● all 19% ] [ ● calorie 22% ] [ ● notes 16% ] [ ● website 16% ]          14:30:00
  1 Dashboard  2 Board  3 Flow  4 Feed  5 Inbox 3                        ● │ acme │ ▶ running │ ⚠ 4
╭────────────────── Agent Tree ──────────────────╮╭────────────── Tasks · needs you ───────────────╮
│ ◆ owner · you                              ⚠ 4 ││ [→] Markdown export N-1             ← ⠋running │
│ ├ ◐ calorie-manager: waits on you · CAL-5   1h ││ [!] Release notes app 1.2 N-3      ← needs you │
│ │ ├ ⏸ calorie-designer: paused waiting… paused ││     ↳ escalation · waits on you: answer m-esc… │
│ │ ├ ● calorie-ios: Build the meal ent… 12m 00s ││ [!] Pick the nutrition data API C… ← needs you │
│ │ │   └ ▸ swift test --filter MealStor… 0m 40s ││     ↳ ask · waits on you: answer m-ask-1 in t… │
│ │ └ ◐ calorie-qa: waits on calorie-ios · C… 4h ││ [◎] Newsletter signup form W-6       in review │
│ └ ◐ manager: waits on you · N-3            30m ││     ↳ queued: busy with N-1                    │
│   ├ ● demo: Pricing page                7m 00s ││                                                │
│   │   └ ✎ Edit: src/pages/pricing.tsx   0m 15s ││                                                │
│   └ ● reviewer: Markdown export         4m 00s ││                                                │
│       └ › Testing the export on a 10k-… 0m 30s ││                                                │
│                                                ││                                                │
╰────────────────────────────────────────────────╯╰────────────────────────────────────────────────╯
╭──────────────────────────────────────────── Activity ────────────────────────────────────────────╮
│ 14:27:30  [reviewer]         Bash          $ go test ./export/...                                │
│ 14:28:00  [owner]            comment       → reviewer  Please check the export on a 10k-line no… │
│ 14:28:00  [calorie-ios]      exit_submit   reviewer=calorie-manager summary="meal entry screen,… │
│ 14:28:02  [calorie-ios]      refused       exit guard refused the turn's end: submit needs a pu… │
│ 14:28:30  [reviewer]         task_comments task=N-1                                              │
│ 14:29:00  [manager]          answer        → demo  20%, rounded to whole dollars.                │
│ 14:29:00  [demo]             Bash          $ npx playwright test pricing.spec.ts                 │
│ 14:29:20  [calorie-ios]      Bash          $ swift test --filter MealStoreTests                  │
│ 14:29:30  [reviewer]         says          “Testing the export on a 10k-line note as the owner … │
│ 14:29:45  [demo]             Edit          src/pages/pricing.tsx                                 │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
 → 12:55:00 calorie-manager asks: Which nutrition data API may we pay for?… [A answer]  +2 in inbox
 h/l panel  j/k move  enter open  z zoom  ^k palette  [ ] project  ? help  q quit
```

## 2 Board

Columns by state — Backlog · Ready · Running · Waiting · In review · Done (Cancelled with `z`) — each a
panel with its color dot and count; cards inside with a thin border in the column color (the focused card
has a heavy accent border). A card: status dot (pulsing when its run is live) and title (`⚠` when it waits on
you), id, project chip and priority marker (`▲` ≥ 8, `△` ≥ 5, `▽`), assignee chip with today's cost of the
task and its age, and one live line: the run's current step with a spinner, what it waits on, `↳ you: …`,
or `✓ done 3h ago`. Arrows/`hjkl` move between columns and cards; columns scroll horizontally when they do
not fit (`◀ more` / `more ▶`). `/` filters (`a:AGENT s:STATUS ! open words`), `!` keeps what waits on you.

`enter` opens the card as a modal: state, origin (who created it from which task and run), why it waits and
who clears it, pending triggers, active parks, dependencies, children, the body, every run with outcome,
exit and cost, comments and recent events — with the actions: `p` pause · `u` resume · `r` retry · `n` run
· `x` cancel (cascading to open children) · `a` reassign · `P` priority · `c` comment · `d` directive
(`!text` is urgent and stops the live run) · `L` live run · `f` flow.

```
 21 tasks  · z shows cancelled                                                                more ▶
╭───────── ● Backlog 3 ─────────╮╭──────── ● Ready 4 +1 ─────────╮╭───────── ● Running 2 ──────────╮
│ ╭───────────────────────────╮ ││ ╭───────────────────────────╮ ││ ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓ │
│ │ ● Blog engine             │ ││ │ ● Crash on empty meal li… │ ││ ┃ ● Pricing page             ┃ │
│ │ W-4  website           ▽3 │ ││ │ CAL-9  calorie         ▲8 │ ││ ┃ W-2  website            ▲8 ┃ │
│ │ ● demo                 1h │ ││ │ ● calorie-qa          25m │ ││ ┃ ● demo                  7m ┃ │
│ │ ○ not released            │ ││ │ ◌ ready · next free slot  │ ││ ┃ ⠋ ✎ Edit: src/pages/prici… ┃ │
│ ╰───────────────────────────╯ ││ ╰───────────────────────────╯ ││ ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛ │
│ ╭───────────────────────────╮ ││ ╭───────────────────────────╮ ││ ╭────────────────────────────╮ │
│ │ ● Dark mode icons         │ ││ │ ● Fix the sync conflict … │ ││ │ ● Build the meal entry sc… │ │
│ │ N-4  notes             ▽3 │ ││ │ N-2  notes             △7 │ ││ │ CAL-3  calorie          △7 │ │
│ │ ● calorie-designer     3h │ ││ │ ● demo        $0.87 · 45m │ ││ │ ● calorie-ios  $2.44 · 12m │ │
│ │ ○ not released            │ ││ │ ↳ backoff: retry at 14:34 │ ││ │ ⠋ ▸ swift test --filter M… │ │
│ ╰───────────────────────────╯ ││ ╰───────────────────────────╯ ││ ╰────────────────────────────╯ │
```

## Run (from a card, the tree or the palette)

A chat-like transcript streamed from `GET /runs/{id}/events/stream`: the agent's messages as bubbles in its
color, tool calls as compact rows (`▸` shell commands in a code style, `⇄` Factory MCP calls with `k=v`
arguments, `✎` file tools), refusals in red, Core's verdict on each Factory call (`◇ core … ok`, a 4xx is a
refusal), the exit as a green `⏏ EXIT` badge and the end of the run. Follow mode keeps the newest line in
view (`f`; scrolling up pauses it, `G` resumes); while the run is live the last line shows the agent at work.
At 110 columns a side panel shows the run's stats (context gauge, tokens, cache, cost, duration, steps,
refusals).

```
╭────────────────────────── calorie-ios · r-ios-7 · CAL-3 · ● live · follow ───────────────────────────╮╭─────────── Run stats ────────────╮
│  ── session started, model claude-opus-4-7 ──                                               14:18:00 ││ Agent      calorie-ios           │
│                                                                                                      ││ Task       CAL-3                 │
│  ╭────────────────────────── calorie-ios 14:18:05 ──────────────────────────╮                        ││ Status     RUNNING               │
│  │ Resuming CAL-3. The owner asked for offline-first, so I'll start from    │                        ││ Model      claude-opus-4-7       │
│  │ the store layer.                                                         │                        ││ Context    █████░░░░░░░░░  34%   │
│  ╰──────────────────────────────────────────────────────────────────────────╯                        ││ Tokens     45,230 in 12,891 out  │
│    ⇄ factory_task_get  task=CAL-2                                                           14:18:30 ││ Cache R/W  23,102 / 4,620        │
│    ◇ core factory_task_get ok                                                               14:18:30 ││ Cost       $1.24                 │
│    ✎ Read  Calorie/MealEntry/MealStore.swift                                                14:19:00 ││ Duration   12m 00s               │
│    ✎ Edit  Calorie/MealEntry/MealStore.swift                                                14:20:00 ││ Steps      20                    │
│    ▸  $ xcodebuild -scheme Calorie -destination 'platform=iOS Simulator,name=iPhone 16'…    14:20:40 ││ Shell      4                     │
│    ▸  $ git push --force origin af/CAL-3                                                    14:21:20 ││ Tools      6                     │
│    ✗ command refused (deny, rule no-force-push): git push --force origin af/CAL-3           14:21:21 ││ Refusals   3                     │
│    ⚠ tool error: command refused by policy no-force-push                                    14:21:22 ││                                  │
│                                                                                                      ││                                  │
│  ╭────────────────────────── calorie-ios 14:22:00 ──────────────────────────╮                        ││                                  │
│  │ Force push is not allowed; pushing a new commit instead.                 │                        ││                                  │
│  ╰──────────────────────────────────────────────────────────────────────────╯                        ││                                  │
│    ▸  $ git push origin af/CAL-3                                                            14:22:10 ││                                  │
│    ⇄ factory_message_send  body="Portion picker done in grams; starting the offline cache.… 14:23:40 ││                                  │
│    ◇ core factory_message_send ok                                                           14:23:40 ││                                  │
│    ✎ Grep  "refreshMeals" in Calorie/**                                                     14:25:00 ││                                  │
│                                                                                                      ││                                  │
│  ╭────────────────────────── calorie-ios 14:27:30 ──────────────────────────╮                        ││                                  │
│  │ All 42 tests pass. Submitting for review.                                │                        ││                                  │
```

## 3 Flow — who passes what to whom

Swimlanes: one lane per party (you, the agents in `reports_to` order, Core), time downwards, and an arrow
from lane to lane for each step: task creation, exits (submit, handoff, review verdicts, done, asked,
blocked), messages (questions up the chain, answers, escalations, forwards), directives, comments,
reassignments, escalations and approval requests — with the verb, task and text beside it. `a` / `t` narrow
it to an agent or a task (the project is the header chip); `g` switches to pairs: every from → to edge with
its count bar, kinds and last time, next to the `reports_to` tree. When the lanes do not fit, a list.

```
╭────────────── Flow · swimlanes · 13 steps · project calorie · agent all · task all ──────────────╮
│          owner         calorie-designer       calorie-qa                                         │
│               calorie-manager       calorie-ios                                                  │
│ 09:30      ●─────────▶         │         │         │     create    CAL-1  “Ship calorie calcula… │
│ 09:50      │         ●─────────▶         │         │     create    CAL-2  “Design the meal entr… │
│ 10:00      │         ●───────────────────▶         │     create    CAL-3  “Build the meal entry… │
│ 10:05      │         ●─────────────────────────────▶     create    CAL-4  “Test meal entry on i… │
│ 11:00      │         │         ◆         │         │     done      CAL-2  meal entry mockups in… │
│ 11:10      │         ◆         │         │         │     create    CAL-5  “Pick the nutrition d… │
│ 11:30      │         ●───────────────────▶         │     create    CAL-7  “Barcode scanner spik… │
│ 11:50      │         ◀───────────────────●         │     question  CAL-3  Should the portion pi… │
│ 12:00      │         ●───────────────────▶         │     answer    CAL-3  Grams only for v1.     │
│ 12:50      │         ◆         │         │         │     asked     CAL-5  Which nutrition data … │
│ 12:55      ◀─────────●         │         │         │     question  CAL-5  Which nutrition data … │
│ 13:20      ●─────────────────────────────▶         │     directive CAL-3  Keep the entry screen… │
│ 14:05      │         │         │         ●─────────▶     create    CAL-9  “Crash on empty meal … │
```

## 4 Feed and 5 Inbox

The **feed** (`GET /feed/stream`, the last 3000 rows) with an icon per event, agents in their colors and
attention rows highlighted; `!` keeps only them, `/` filters, scrolling up pauses it and `G` returns to
the newest. The **inbox** lists questions, escalations and approvals (`GET /inbox`, `GET /approvals`) beside
the selected item: its body, the approval's call and arguments with `y approve` / `n reject`, or the
question's options as pills (`←`/`→`, `enter`), `a` for free text.

## Behaviour

- Refresh: the overview (`/health`, `/status`, `/tasks`, `/pauses`, `/pending/count`, `/nodes`, `/inbox`,
  `/approvals`, `/costs` by agent today, by task, and the runs of the last 24 h, plus each live run's record
  for its usage) every 3 s, one read at a time; the agent catalog once. Only `/health` and `/status` failing
  marks Core unreachable (the header shows it and how old the data is).
- Streams — the feed, each live run (its last 20 steps, then live) and the opened run — reconnect after an
  error with `Last-Event-ID` (1 s backoff doubling to 15 s) and deliver each event once; a 400/401/404 is
  not retried.
- The token is sent only in the `Authorization` header; it is not part of a URL, an error or the screen.
- Every frame is exactly the terminal's size and follows resizes; 100×30 is the designed size, 80×24 works,
  below 60×16 the UI asks for a larger terminal. The palette has a light and a dark variant: the UI asks the
  terminal for its background (`tea.RequestBackgroundColor`) and switches; Bubble Tea downsamples colors to
  what the terminal supports.

## Core API used and added

Added for the UI: `GET /runs/{id}/events/stream` — a run's events as server-sent events (`id` = the event
sequence; resume with `Last-Event-ID` or `?after=`; `?tail=N` starts at the last N events; the worker lease
is left out; after an ended run's last event `event: end` with `{"status","outcome"}` closes the stream). It
shares its loop with `GET /feed/stream`. Message feed rows now carry `sender → recipient (type): body` as
their summary, which the flow, the activity stream and the feed show.

## Tests

`go test ./internal/tui`: the HTTP client against an `httptest` fake Core (overview, partial failures, task
detail, every action's route and body, stream reconnect with `Last-Event-ID`, run stream end, 404 give-up,
SSE parsing, recording without secrets), model updates (board navigation and filters, confirmations,
inputs, the card modal, the prompt bar, inbox answers and approvals, kill switch, run follow mode, feed
dedup and toasts, dashboard panels and zoom, the palette, mouse, resize and the light theme) and golden
screens of every view at fixed sizes (`internal/tui/testdata/*.golden`; `go test ./internal/tui -update`
rewrites them). The Core endpoint is tested in `internal/core/streams_test.go` (Postgres).
