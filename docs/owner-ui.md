# Owner UI (`factory ui`)

A full-screen terminal UI for the owner: what every agent is doing right now, the work as a Kanban board,
a live transcript of any run and its full runtime session, who passes what to whom, the feed, the inbox and a
chat with the owner's direct reports — with every owner action in place, tool errors counted, pull requests
and artifacts one key away, and a desktop notification when something waits on you. It is a client of Core's owner API (the same token as the CLI) and keeps no state.

```sh
export FACTORY_URL=http://127.0.0.1:8080 FACTORY_TOKEN=...   # as for the other factory commands
./bin/factory ui                      # live, against Core
./bin/factory ui --demo               # a built-in demo company, no Core needed
./bin/factory ui --record state.json  # snapshot Core's current state into a fixture file (0600)
./bin/factory ui --fixture state.json # browse a recorded fixture, no Core needed
./bin/factory ui --notify bell        # desktop (default: a macOS notification) | bell | off
./bin/factory ui --claude-config-dir ~/.factory-node/claude --codex-home ~/.factory-node/codex  # local transcripts
```

In `--demo` and `--fixture` mode actions are not sent anywhere (the footer says `fixture: not sent — …`);
run views replay the recorded events. A recording holds the overview, the last 2000 feed rows, the detail
of every open task (and up to 50 closed ones), and the events and tool calls of the live runs and latest
attempts, and the owner conversations; run leases are left out and the token is never written.

Built with Bubble Tea 2.0.9, Bubbles 2.2.1 (textinput, textarea, viewport, spinner) and Lip Gloss 2.0.6
(`charm.land/...` v2 modules, Go 1.25+). Code: `internal/tui` — the `Client` interface with its HTTP and
fixture implementations is separate from the views — and `cmd/factory/ui.go`.

## Layout

- **Header**: the app name, one chip per project — `[ ● project-1 22% ]`: the dot is green while the project
  has live runs, the percentage is its done share; the selected chip (`[` / `]`, or a click) narrows every
  screen to that project — and a live clock.
- **Tabs**: `1 Dashboard  2 Board  3 Flow  4 Feed  5 Inbox  6 Chat` (unread messages `•1`), then Core's connection dot, the company,
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
 ◆ APP FACTORY  [ ● all 19% ] [ ● project-1 22% ] [ ● project-2 16% ] [ ● project-3 16% ]  14:30:00
  1 Dashboard  2 Board  3 Flow  4 Feed  5 Inbox 3                   ● │ Company 1 │ ▶ running │ ⚠ 4
╭────────────────── Agent Tree ──────────────────╮╭────────────── Tasks · needs you ───────────────╮
│ ◆ owner · you                              ⚠ 4 ││ [→] Task 1 P2-1                     ← ⠋running │
│ └ ◐ pm: waits on you · P1-5                 1h ││ [!] Task 3 P2-3                    ← needs you │
│   ├ ⏸ designer: paused waiting for the… paused ││     ↳ escalation · waits on you: answer m-esc… │
│   ├ ● developer-1: Task 3              12m 00s ││ [!] Task 5 P1-5                    ← needs you │
│   │   └ ▸ go test ./src/... -run TestS… 0m 40s ││     ↳ ask · waits on you: answer m-ask-1 in t… │
│   ├ ◐ lead: waits on you · P2-3            30m ││ [◎] Task 6 P3-6                      in review │
│   │ ├ ● developer-2: Task 2             7m 00s ││     ↳ queued: busy with P2-1                   │
│   │ │   └ ✎ Edit: src/module_2.ts       0m 15s ││                                                │
│   │ └ ● reviewer: Task 1                4m 00s ││                                                │
│   │     └ › Testing Task 1 on a large … 0m 30s ││                                                │
│   └ ◐ qa: waits on developer-1 · P1-4       4h ││                                                │
│                                                ││                                                │
╰────────────────────────────────────────────────╯╰────────────────────────────────────────────────╯
╭──────────────────────────────────────────── Activity ────────────────────────────────────────────╮
│ 14:27:30  [reviewer]    Bash          $ go test ./...                                            │
│ 14:28:00  [owner]       comment       → reviewer  Please check Task 1 on a large input.          │
│ 14:28:00  [developer-1] exit_submit   reviewer=pm summary="module 1 with an offline store, 42 t… │
│ 14:28:02  [developer-1] refused       exit guard refused the turn's end: submit needs a pushed … │
│ 14:28:30  [reviewer]    task_comments task=P2-1                                                  │
│ 14:29:00  [lead]        answer        → developer-2  Option B, keep it behind a flag.            │
│ 14:29:00  [developer-2] Bash          $ npm test -- module_2                                     │
│ 14:29:20  [developer-1] Bash          $ go test ./src/... -run TestStore                         │
│ 14:29:30  [reviewer]    says          “Testing Task 1 on a large input as the owner asked.”      │
│ 14:29:45  [developer-2] Edit          src/module_2.ts                                            │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
 → 12:55:00 pm asks: Which option for Task 5: A or B? A is fr… [A option-a / option-b]  +2 in inbox
 h/l panel  j/k move  enter open  z zoom  ^k palette  [ ] project  ? help  q quit
```

## 2 Board

Columns by state — Backlog · Ready · Running · Waiting · In review · Done (Cancelled with `z`) — each a
panel with its color dot and count; cards inside with a thin border in the column color (the focused card
has a heavy accent border). A card: status dot (pulsing when its run is live) and title (`⚠` when it waits on
you), id, project chip and priority marker (`▲` ≥ 8, `△` ≥ 5, `▽`), assignee chip with today's cost of the
task and its age, and one live line: the run's current step with a spinner, what it waits on, `↳ you: …`,
or `✓ done 3h ago`. Arrows/`hjkl` move between columns and cards; columns scroll horizontally when they do
not fit (`◀ more` / `more ▶`). `/` filters (`a:AGENT s:STATUS ! open words`), `!` keeps what waits on you,
`n` creates a task (below).

`enter` opens the card as a modal: state, origin (who created it from which task and run), why it waits and
who clears it, pending triggers, active parks, dependencies, children, the body, every run with outcome,
exit and cost, comments and recent events — with the actions: `p` pause · `u` resume · `r` retry · `n` run
· `x` cancel (cascading to open children) · `a` reassign · `P` priority · `c` comment · `d` directive
(`!text` is urgent and stops the live run) · `L` live run · `t` transcript · `o` open the PR · `f` flow.

```
 21 tasks  · z shows cancelled                                                                more ▶
╭───────── ● Backlog 3 ─────────╮╭──────── ● Ready 4 +1 ─────────╮╭───────── ● Running 2 ──────────╮
│ ╭───────────────────────────╮ ││ ╭───────────────────────────╮ ││ ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓ │
│ │ ● Task 4                  │ ││ │ ● Task 9                  │ ││ ┃ ● Task 2                   ┃ │
│ │ P3-4  project-3        ▽3 │ ││ │ P1-9  project-1        ▲8 │ ││ ┃ P3-2  project-3         ▲8 ┃ │
│ │ ● developer-2          1h │ ││ │ ● qa                  25m │ ││ ┃ ● developer-2           7m ┃ │
│ │ ○ not released            │ ││ │ ◌ ready · next free slot  │ ││ ┃ ⠋ ✎ Edit: src/module_2.ts  ┃ │
│ ╰───────────────────────────╯ ││ ╰───────────────────────────╯ ││ ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛ │
│ ╭───────────────────────────╮ ││ ╭───────────────────────────╮ ││ ╭────────────────────────────╮ │
│ │ ● Task 4                  │ ││ │ ● Task 2                  │ ││ │ ● Task 3                   │ │
│ │ P2-4  project-2        ▽3 │ ││ │ P2-2  project-2        △7 │ ││ │ P1-3  project-1         △7 │ │
│ │ ● designer             3h │ ││ │ ● developer-2 $0.87 · 45m │ ││ │ ● developer-1  $2.44 · 12m │ │
│ │ ○ not released            │ ││ │ ↳ backoff: retry at 14:34 │ ││ │ ⠋ ▸ go test ./src/... -ru… │ │
│ ╰───────────────────────────╯ ││ ╰───────────────────────────╯ ││ ╰────────────────────────────╯ │
```

## Run (from a card, the tree or the palette)

A chat-like transcript streamed from `GET /runs/{id}/events/stream`: the agent's messages as bubbles in its
color, tool calls as compact rows (`▸` shell commands in a code style, `⇄` Factory MCP calls with `k=v`
arguments, `✎` file tools), refusals in red, Core's verdict on each Factory call (`◇ core … ok`, a 4xx is a
refusal), the exit as a green `⏏ EXIT` badge and the end of the run. Follow mode keeps the newest line in
view (`f`; scrolling up pauses it, `G` resumes); while the run is live the last line shows the agent at work.
At 110 columns a side panel shows the run's stats (context gauge, tokens, cache, cost, duration, steps,
refusals, the tool error count and the last error). `t` opens the full transcript, `e` keeps only errors and
refusals, `T` opens the task.

```
╭─────────────────────────── developer-1 · r-d1-7 · P1-3 · ● live · follow ────────────────────────────╮╭─────────── Run stats ────────────╮
│  ── session started, model claude-opus-4-7 ──                                               14:18:00 ││ Agent      developer-1           │
│                                                                                                      ││ Task       P1-3                  │
│  ╭────────────────────────── developer-1 14:18:05 ──────────────────────────╮                        ││ Status     RUNNING               │
│  │ Resuming P1-3. The owner asked for offline support, so I'll start with   │                        ││ Model      claude-opus-4-7       │
│  │ the store layer.                                                         │                        ││ Context    █████░░░░░░░░░  34%   │
│  ╰──────────────────────────────────────────────────────────────────────────╯                        ││ Tokens     45,230 in 12,891 out  │
│    ⇄ factory_task_get  task=P1-2                                                            14:18:30 ││ Cache R/W  23,102 / 4,620        │
│    ◇ core factory_task_get ok                                                               14:18:30 ││ Cost       $1.24                 │
│    ✎ Read  src/module_1.go                                                                  14:19:00 ││ Duration   12m 00s               │
│    ✎ Edit  src/module_1.go                                                                  14:20:00 ││ Steps      20                    │
│    ▸  $ go test ./... -run TestModule1 -count=1 -race -timeout 5m                           14:20:40 ││ Shell      4                     │
│    ▸  $ git push --force origin af/P1-3                                                     14:21:20 ││ Tools      6                     │
│    ✗ command refused (deny, rule no-force-push): git push --force origin af/P1-3            14:21:21 ││ Refusals   3                     │
│    ⚠ tool error: command refused by policy no-force-push                                    14:21:22 ││                                  │
│                                                                                                      ││                                  │
│  ╭────────────────────────── developer-1 14:22:00 ──────────────────────────╮                        ││                                  │
│  │ Force push is not allowed; pushing a new commit instead.                 │                        ││                                  │
│  ╰──────────────────────────────────────────────────────────────────────────╯                        ││                                  │
│    ▸  $ git push origin af/P1-3                                                             14:22:10 ││                                  │
│    ⇄ factory_message_send  body="Task 3: the store layer is done; starting the offline cac… 14:23:40 ││                                  │
│    ◇ core factory_message_send ok                                                           14:23:40 ││                                  │
│    ✎ Grep  "loadItems" in src/**                                                            14:25:00 ││                                  │
│                                                                                                      ││                                  │
│  ╭────────────────────────── developer-1 14:27:30 ──────────────────────────╮                        ││                                  │
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
╭───────────── Flow · swimlanes · 13 steps · project project-1 · agent all · task all ─────────────╮
│          owner             designer               qa                                             │
│                     pm              developer-1                                                  │
│ 09:30      ●─────────▶         │         │         │     create    P1-1   “Task 1”               │
│ 09:50      │         ●─────────▶         │         │     create    P1-2   “Task 2”               │
│ 10:00      │         ●───────────────────▶         │     create    P1-3   “Task 3”               │
│ 10:05      │         ●─────────────────────────────▶     create    P1-4   “Task 4”               │
│ 11:00      │         │         ◆         │         │     done      P1-2   module 1 mockups v3    │
│ 11:10      │         ◆         │         │         │     create    P1-5   “Task 5”               │
│ 11:30      │         ●───────────────────▶         │     create    P1-7   “Task 7”               │
│ 11:50      │         ◀───────────────────●         │     question  P1-3   Should Task 3 support… │
│ 12:00      │         ●───────────────────▶         │     answer    P1-3   Option A only for now. │
│ 12:50      │         ◆         │         │         │     asked     P1-5   Which option for Task… │
│ 12:55      ◀─────────●         │         │         │     question  P1-5   Which option for Task… │
│ 13:20      ●─────────────────────────────▶         │     directive P1-3   Keep Task 3 working o… │
│ 14:05      │         │         │         ●─────────▶     create    P1-9   “Task 9”               │
```

## 4 Feed and 5 Inbox

The **feed** (`GET /feed/stream`, the last 3000 rows) with an icon per event, agents in their colors and
attention rows highlighted; `!` keeps only them, `/` filters, scrolling up pauses it and `G` returns to
the newest. The **inbox** lists questions, escalations and approvals (`GET /inbox`, `GET /approvals`) beside
the selected item: its body, the approval's call and arguments with `y approve` / `n reject`, or the
question's options as pills (`←`/`→`, `enter`), `a` for free text.

## 6 Chat — the owner's direct reports

The agents that report to the owner (`reports_to: owner`) in the selected project (the header chip; the first
project when all are shown), `★` the default report (`owner.default_report`, from `GET /health`). Each has
one conversation, `owner-<agent>-<project>` (`GET /conversations/{id}/messages`, oldest first): the agent's
messages as bubbles in its color on the left, yours in the accent on the right; a question still waiting on
you says so, structured questions show their options and answers. `enter` writes, `enter` sends through
`POST /messages` (no confirmation: it is a conversation) — to the selected report, with `@name` added when it
is not the default report; a message that starts with `@name` goes to that report and switches to it, a name
that is not a direct report is refused before anything is sent. While the agent's conversation task has a
live run the list and the conversation say `lead is replying…`; `t` opens that run's transcript. Unread
markers (`•1` on the report and on the tab) count the agent's messages newer than your last message and than
what this session showed you; every refresh rereads all owner conversations.

```
╭──────── Direct reports ────────╮╭─────────────────────────────────────────────── lead · owner-lead-project-2 ────────────────────────────────────────────────╮
│ project  project-2             ││                                                                                                                            │
│ [ ] switches project           ││                                                                                                                            │
│                                ││                                                                                                                            │
│ ● pm ★                         ││                                                                                                                            │
│   project manager              ││                                                                                                                            │
│ ● lead                         ││                                                                                                                            │
│   ⠋ replying…                  ││                                                                                                                            │
│                                ││                                                                                                                            │
│ ★ default report               ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                                                            │
│                                ││                                                                                         ╭─────── you 10-20 13:50 ────────╮ │
│                                ││                                                                                         │ @lead is release 1.2 on track? │ │
│                                ││                                                                                         ╰────────────────────────────────╯ │
│                                ││                                                                                                                            │
│                                ││ ╭────────────────────────────────── lead 10-20 13:55 ───────────────────────────────────╮                                  │
│                                ││ │ Yes: it passed review and I asked for your approval to publish it to beta. P2-2 still │                                  │
│                                ││ │ fails, it waits on a rebase.                                                          │                                  │
│                                ││ ╰───────────────────────────────────────────────────────────────────────────────────────╯                                  │
│                                ││                                                                                                                            │
│                                ││                                              ╭───────────────────────────── you 10-20 14:28 ─────────────────────────────╮ │
│                                ││                                              │ @lead can P2-2 be retried after the rebase, or should we cut it from 1.2? │ │
│                                ││                                              ╰───────────────────────────────────────────────────────────────────────────╯ │
│                                ││  ⠋ lead is replying                                                                                                        │
│                                ││ ╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮ │
│                                ││ │ enter to write to lead · @name addresses another report                                                                │ │
│                                ││ ╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯ │
```

## Transcript — the session drill-down (`t` on a run)

`t` in the run view (also in the live run picker, in the task detail and in the chat for a reply) opens the
run's full runtime transcript: the agent's text as bubbles, reasoning summaries (`∴ reasoning`: Claude's
thinking blocks, Codex's reasoning summaries; two lines until expanded), every tool call with its full input
(the command of a shell call, indented JSON otherwise) and its full result, refusals and errors in red, the
end of the run. A call without a result yet is `running`. `j`/`k` select an entry, `enter` expands or
collapses it, `E` expands everything, `e` keeps only tool errors and refusals, `f` follows the newest entry
(live-tailed from `GET /runs/{id}/events/stream` while the run runs), `T` opens the task, `esc` goes back.

**Sources.** Core keeps the runtime's whole event stream as run events — Claude's stream-json messages
(`claude/assistant`, `claude/user`: text, thinking, `tool_use` with the full input, `tool_result` with the full
content and `is_error`) and Codex's app-server notifications (`item/started`, `item/completed`: agent messages,
reasoning, command executions with `aggregatedOutput` and `exitCode`, MCP calls with result or error, file
changes) — redacted by the node before they leave it (core-owned-project-data D6; the uploaded run logs,
`GET /runs/{id}/logs`, hold the same lines). That is the transcript's source (`core events` badge), with
Core's refusals of Factory MCP calls from `GET /runs/{id}/tool-calls`. When the UI runs on the node's machine
it also finds the runtime's own session file — Claude `$CLAUDE_CONFIG_DIR/projects/*/<session_id>.jsonl`,
Codex `$CODEX_HOME/sessions/**/rollout-…-<thread id>.jsonl` (`--claude-config-dir`, `--codex-home`; defaults
`~/.claude`, `~/.codex`) — and `l` switches to it (`local transcript` badge; used by itself when Core has no
events of the run); elsewhere the header says `local: not available here`. A local file is read with the
node's redaction rules (the values of credential-named environment variables, URL passwords and common token
shapes become `[REDACTED]`); subagent side chains and the brief prompt are left out.

```
╭────────────────────────────── developer-1 · r-d1-7 · P1-3 · ● live ──────────────────────────────╮
│   core events  · local: not available here  ✗3                                       paused · G  │
│ ──────────────────────────────────────────────────────────────────────────────────────────────── │
│   ── session started, model claude-opus-4-7 ──                                          14:18:00 │
│   ∴ reasoning                                                                           14:18:04 │
│     The directive says offline first. The store layer is the base of everything else, so the     │
│     cache can wait until the store has tests.                                                    │
│ ▌ ╭───────────────────────────────── developer-1 14:18:05 ─────────────────────────────────╮     │
│ ▌ │ Resuming P1-3. The owner asked for offline support, so I'll start with the store       │     │
│ ▌ │ layer.                                                                                 │     │
│ ▌ ╰────────────────────────────────────────────────────────────────────────────────────────╯     │
│   ▾ ⇄ factory_task_get  task=P1-2                                                     ✓ 14:18:30 │
│       input                                                                                      │
│       │ {                                                                                        │
│       │   "task": "P1-2"                                                                         │
│       │ }                                                                                        │
│       result · 1 line                                                                            │
│       │ {"id":"P1-2","title":"Task 2","status":"DONE","result":"mockups ready"}                  │
│   ▾ ✎ Read  src/module_1.go                                                           ✓ 14:19:00 │
│       input                                                                                      │
│       │ {                                                                                        │
│       │   "file_path": "src/module_1.go"                                                         │
│       │ }                                                                                        │
│       result · 6 lines                                                                           │
│  ✗ last error 14:28:02 exit guard refused the turn's end: submit needs a pushed branch ahead…    │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
```

## Tool errors

Each run's tool errors and refusals are counted from its transcript: tool results with `is_error` (a failed
shell command, a failed MCP call), command guard refusals (exit 126; the refusal event and the refused
command's failed result are one incident), exit guard refusals, and Core's refusals of Factory MCP calls
(tool calls with status ≥ 400; the runtime's failed result of that call is the same incident). The count is a
red badge — `✗3` — on the agent tree row, the task card and the run and transcript headers; the selected tree
row and the selected card show the last error, the run's stats panel shows it in full. Live runs are recounted
every 15 s and at once when their tail shows a refusal or an error; ended runs are counted when a task detail
lists them. `e` in the run view and in the transcript keeps only the errors.

```
╭──────────────────── developer-1 · r-d1-7 · P1-3 · ● live ────────────────────╮
│   core events  · local: not available here  ✗3   errors only         follow  │
│ ──────────────────────────────────────────────────────────────────────────── │
│   ▸ ❯ Bash  $ git push --force origin af/P1-3               ✗ error 14:21:20 │
│       Exit code 126                                                          │
│   ✗ command refused (deny, rule no-force-push): git push --force o… 14:21:21 │
│   ▸ ⇄ factory_exit_submit  reviewer=pm summary="module 1 w… ✗ error 14:28:00 │
│       409 {"reason":"branch_not_pushed","hint":"push af/P1-3 ahead of mai…   │
│   ✗ Core refused factory_exit_submit (409)                          14:28:01 │
│ ▌ ✗ exit guard refused the turn's end: submit needs a pushed branc… 14:28:02 │
│                                                                              │
```

## External links

The task detail shows the pull request of the task's latest exit (`GET /tasks/{id}/exits`: the node's git and
forge facts — number, state, head) and the checks of its submission; `o` opens it in the browser (`open` on
macOS, `xdg-open` elsewhere; the forge's URL, else one derived from the project's GitHub or GitLab
repository). In the inbox an artifact approval shows the artifact (id, version, file name, size, digest);
`v` previews it before you decide: `GET /projects/{p}/artifacts/{a}/{v}/content` is downloaded, markdown is
rendered (headings, lists, emphasis, quotes, code blocks) and text shown in a viewport inside the UI, `o` there
writes it to a private temp file and opens it with the system viewer, `y`/`n` approve or reject from the
preview; anything not text (an image, a PDF) is opened with the system viewer right away; an external
reference (`https:`) opens in the browser. (glamour v2 needs Go 1.25.8 in `go.mod` while the build image pins
1.25.0, so the preview uses its own small renderer.)

```
╭────────────────────────────────────────────── P2-1 · Task 1 ───────────────────────────────────────────────╮
│ ● REVIEW  IN_REVIEW   project-2   ● reviewer  priority 6 · v3 · attempt 0                                  │
│ origin  created by lead from P2-3 · implementer developer-2 · reviewer reviewer · 4h ago                   │
│ PR      #12 open · head 9f3c2e1… · https://github.com/company-1/project-2/pull/12  o opens                 │
│ checks  ✓ go test ./... · ✓ go vet ./...                                                                   │
│ why     live_run — run r-rev-2 is running                                                                  │
```

```
╭─╭───────────── Preview · release-notes v2 · release-notes.md ──────────────╮─╮
│ │  Project 2 · release 1.2                                                 │ │
│ │                                                                          │ │
│ │ What's new                                                               │ │
│ │                                                                          │ │
│ │ • Faster sync: changes reach other devices in under a second.            │ │
│ │ • Offline mode for module 1 (see P1-3).                                  │ │
│ │ • A new empty state for the item list.                                   │ │
│ │                                                                          │ │
╰─│ Fixed                                                                    │─╯
╭─│                                                                          │─╮
│ │ 1.  Large inputs no longer time out on import.                           │ │
│ │ 2.  The settings screen keeps its scroll position.                       │ │
│ │                                                                          │ │
│ │ ▌ Beta channel first; the stable release follows after a week without    │ │
│ │ ▌ regressions.                                                           │ │
│ │ ──────────────────────────────────────────────────────────────────────── │ │
│ │ j/k scroll o open in viewer y approve n reject esc close 20 lines ·   0% │ │
```

## New task (`n` on the board)

A form: project (`←`/`→`, the selected project first), title, description (a textarea; `ctrl+e` edits it in
`$VISUAL`/`$EDITOR`), assignee (the catalog's agents, or unassigned), priority (-1000..1000), dependencies
(the project's open tasks: `←`/`→` move, `space` toggles) and ready now (released at once, or kept in the
backlog). `tab`/`shift+tab` move between fields, `ctrl+s` (or `enter` on the button) creates it with
`POST /tasks` (`ready: true` when ready now; a `request_id` per submit). Missing or invalid fields are shown at
the field before anything is sent; Core's refusal is shown under the form, which stays open.

```
╭─────────── ● Backlog 3 +1 ───────────╮╭──────────── ● Ready 4 +2 ────────────╮
│ ╭──────────────────────────────── New task ────────────────────────────────╮ │
│ │   Project       project-1                                                │ │
│ │ ▸ Title         what needs doing                                         │ │
│ │                 ✗ a title is required                                    │ │
│ │   Description   details, acceptance criteria (ctrl+e opens $EDITOR)      │ │
│ │                                                                          │ │
│ │   Assignee      unassigned                                               │ │
│ │                 ✗ a ready task needs an assignee                         │ │
│ │   Priority      5          -1000..1000, higher runs first                │ │
│ │   Depends on    ☐ P1-1  ☐ P1-3  ☐ P1-4  ☐ P1-5  ☐ P1-6  ☐ P1-7  ☐ P1-9   │ │
│ │   Ready now     ● yes — released to the board                            │ │
│ │                                                                          │ │
│ │                 Create task                                              │ │
│ │ ──────────────────────────────────────────────────────────────────────── │ │
│ │ tab next ←→ choose space toggle ctrl+e $EDITOR ctrl+s create esc cancel  │ │
```

## Notifications

A new attention row for the owner in the feed — a question or escalation that reached you, an approval
request — shows the toast and, by default, a macOS notification (`osascript -e 'display notification …'`,
title `App Factory · Question for you|Approval requested|Escalation`, the project as subtitle). Elsewhere, or
when `osascript` fails, the terminal rings its bell. `--notify=desktop|bell|off` chooses (there is no UI config
file; the flag is the setting). Each row notifies once, at most one notification per 10 s (the rows held back
are counted into the next: `(+2 more)`); the replay of history at startup does not notify.

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

Also added: `GET /tasks/{id}/exits` — the task's accepted exits, oldest first, with the node's git and forge
facts (`{run_id, kind, created_at, facts: {head, branch, ahead_of_default, pushed, pr: {number, state,
head_sha, url}}}`; the task detail's pull request), and `default_report` in `GET /health`
(`owner.default_report`, or the only direct report; the chat's `★`). Used as they were: `GET /runs/{id}/events`
(error counts), `GET /conversations/{id}/messages`, `POST /messages`, `POST /tasks`,
`GET /projects/{p}/artifacts/{a}/{v}/content`.

## Tests

`go test ./internal/tui`: the HTTP client against an `httptest` fake Core (overview, partial failures, task
detail, every action's route and body, stream reconnect with `Last-Event-ID`, run stream end, 404 give-up,
SSE parsing, recording without secrets), model updates (board navigation and filters, confirmations,
inputs, the card modal, the prompt bar, inbox answers and approvals, kill switch, run follow mode, feed
dedup and toasts, dashboard panels and zoom, the palette, mouse, resize and the light theme) and golden
screens of every view at fixed sizes (`internal/tui/testdata/*.golden`; `go test ./internal/tui -update`
rewrites them). The Core endpoint is tested in `internal/core/streams_test.go` (Postgres).

The added features are tested in `internal/tui/features_test.go` and `client_test.go`: the transcript parsers on
fixture session files (`testdata/transcripts/claude.jsonl`, `codex.jsonl`), the transcript built from Core's
events and the error tally's deduplication, finding and redacting local session files, the transcript view
(expand, errors only, live events, back), the badges and the recount, the chat (addressing, refusal of an
unknown name, unread markers, the reply indicator), the create task form (validation, the action, Core's
refusal), the notifier with a fake command runner (quoting, dedup, rate limit, bell fallback), external links
with a fake opener (PR URL, the preview, a binary artifact) and the client's new routes against the fake Core;
goldens `chat_*`, `transcript_*`, `run_errors_*`, `task_detail_pr_*`, `inbox_artifact_*`, `preview_*`, `form_*`.
`GET /tasks/{id}/exits` and `default_report` are tested in `internal/core` (Postgres).
