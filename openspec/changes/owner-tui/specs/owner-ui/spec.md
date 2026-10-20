## ADDED Requirements

### Requirement: Owner UI over the owner API
`factory ui` SHALL be a full-screen terminal UI that reads and acts only through the owner API with the owner token, keeps no state of its own, and sends the token only in the `Authorization` header — never in a URL, an error, the screen or a recording.

#### Scenario: Core unreachable
- **WHEN** `/health` and `/status` fail during a refresh
- **THEN** the header SHALL show Core as unreachable with the age of the shown data, and the last data SHALL stay on screen

#### Scenario: Partial failure
- **WHEN** only `/costs` fails during a refresh
- **THEN** Core SHALL NOT be marked unreachable and the other panels SHALL be updated

### Requirement: Dashboard
The dashboard SHALL show the owner's `reports_to` tree with each agent's state (running, waiting — on the owner first —, paused, idle), what it is doing and a timer, and for a running agent its current step; the selected agent's tasks with why each waits; its live run's stats (model, context gauge, tokens, cache, cost, duration, today's cost and runs); one activity stream of every live run's steps and the feed; and a pulse of the board, spend and nodes.

#### Scenario: Running agent
- **WHEN** an agent has a live run whose last event is a shell command
- **THEN** its tree row SHALL show the run's task and run time and a sub-row with the command and its age, and `enter` SHALL open the run

#### Scenario: Context gauge is the last call, not the run total
- **WHEN** a live run's events carry per-call usage (a Claude assistant message's `usage`, a Codex `thread/tokenUsage/updated` `last`)
- **THEN** the context gauge SHALL measure the most recent call's prompt (input + cache read + cache creation; Codex `inputTokens` including cached) against the window the runtime reports (Claude result `modelUsage.contextWindow`, Codex `modelContextWindow`), else the model's known window; it SHALL NOT use the run's cumulative usage, which SHALL be labelled `(run total)`
- **AND** a prompt over the window SHALL show 100% in the warning color, an unknown model SHALL show tokens without a percentage, and a run without per-call usage SHALL show `n/a`

#### Scenario: Owner row
- **WHEN** the owner row is selected
- **THEN** the tasks panel SHALL list what waits on the owner or is in review and the stats SHALL show company totals

### Requirement: Board
The board SHALL show tasks as Kanban columns by state (Backlog, Ready, Running, Waiting, In review, Done; Cancelled on demand) with cards showing title, id, project, priority, assignee, today's cost, age and one live line, filters (`/` with `a:`, `s:`, `!`), and a card modal with state, origin, why it waits and who clears it, triggers, parks, dependencies, children, body, runs with outcome, exit and cost, comments, recent events and the task actions.

#### Scenario: Filter on what waits on the owner
- **WHEN** the owner presses `!` on the board
- **THEN** only cards that wait on the owner SHALL remain

#### Scenario: Columns wider than the terminal
- **WHEN** the columns do not fit the width
- **THEN** they SHALL scroll horizontally with `◀ more` / `more ▶` markers

### Requirement: Flow
The flow screen SHALL show who passes what to whom: swimlanes per party (the owner, agents in `reports_to` order, Core) with an arrow per step — task creation, exits, messages, directives, comments, reassignments, escalations, approval requests — and a pairs view with each from → to edge, its count, kinds and last time; narrowed by project, agent or task.

#### Scenario: Question up the chain
- **WHEN** a feed row reports a question message from developer-1 to pm
- **THEN** the swimlanes SHALL draw an arrow from developer-1's lane to pm's lane with the verb, task and text

#### Scenario: Lanes do not fit
- **WHEN** the terminal is too narrow for the lanes
- **THEN** the flow SHALL be shown as a list

### Requirement: Feed
The feed screen SHALL stream `GET /feed/stream`, deliver each row once, highlight attention rows, filter (`!` attention only, `/` text), pause on scroll up and return to the newest with `G`.

#### Scenario: Reconnect
- **WHEN** the feed stream drops and reconnects with `Last-Event-ID`
- **THEN** no row SHALL be shown twice

### Requirement: Inbox, answers, approvals and preview
The inbox SHALL list questions, escalations and approvals beside the selected item's body. A question SHALL be answerable with one of its options or free text, an approval approved or rejected with a reason, and an artifact approval previewed before deciding: markdown rendered and text shown in a viewport, other content opened with the system viewer. The oldest item waiting on the owner SHALL also be answerable from any screen through the prompt bar.

#### Scenario: Answer a single-choice question
- **WHEN** the owner selects an option pill and confirms
- **THEN** the UI SHALL send the answer with that option and the item SHALL leave the inbox on the next refresh

#### Scenario: Preview an artifact before approving
- **WHEN** the owner presses `v` on an artifact approval for a markdown file
- **THEN** the UI SHALL download its content, render it in a viewport, and `y`/`n` there SHALL approve or reject it

#### Scenario: Binary artifact
- **WHEN** the artifact is not text
- **THEN** the UI SHALL open it with the system viewer instead of rendering it

### Requirement: Live run view
A run view SHALL stream `GET /runs/{id}/events/stream` and show the agent's messages, tool calls (shell, Factory MCP with arguments, file tools), refusals, Core's verdict on each Factory call, the exit and the end of the run, with follow mode (`f`, paused by scrolling up, `G` resumes), an errors-only filter (`e`) and, from 110 columns, a stats panel.

#### Scenario: Run ends while open
- **WHEN** the stream sends `event: end`
- **THEN** the view SHALL show the run's end with its status and outcome and SHALL NOT reconnect

### Requirement: Full transcript
`t` on a run SHALL open its full runtime transcript built from Core's run events and tool calls: text, reasoning, every tool call with its full input and result (collapsible), refusals and errors, live-tailed while the run runs. When the UI runs on the node's machine it SHALL also offer the runtime's local session file (Claude or Codex), read with the node's redaction rules, and use it alone when Core has no events of the run.

#### Scenario: Claude session from Core events
- **WHEN** a Claude run's events contain a `tool_use` and its `tool_result` with `is_error`
- **THEN** the transcript SHALL show one entry with the full input, the result and an error mark

#### Scenario: Local file with a secret
- **WHEN** a local session file contains a credential-named environment value
- **THEN** the transcript SHALL show `[REDACTED]` in its place

#### Scenario: Not on the node's machine
- **WHEN** no local session file is found
- **THEN** the header SHALL say `local: not available here` and Core's events SHALL be shown

### Requirement: Chat with direct reports
The chat screen SHALL list the owner's direct reports in the selected project with the default report marked, show each one's owner conversation, and send the owner's messages through `POST /messages` without a confirmation, addressing the selected report with `@name` when it is not the default one and refusing unknown names before sending.

#### Scenario: Replying and unread
- **WHEN** a report's conversation task has a live run and the report has messages newer than the owner's last one
- **THEN** the chat SHALL say the report is replying and show an unread marker on the report and the tab

#### Scenario: Message to a non-default report
- **WHEN** the owner writes to `lead` while `pm` is the default report
- **THEN** the message SHALL be sent with `@lead` and reach lead's conversation

#### Scenario: Unknown name
- **WHEN** the owner sends `@nobody hello`
- **THEN** the UI SHALL refuse it and send nothing

### Requirement: Tool error counts
The UI SHALL count each run's tool errors and refusals — results with `is_error`, command guard refusals, exit guard refusals and Core's refusals of Factory calls — counting one incident once, and show the count with the last error on the agent tree, task cards and run and transcript headers.

#### Scenario: Guard refusal and failed result
- **WHEN** a command guard refuses a command and the runtime then reports that command's failed result
- **THEN** the count SHALL increase by one

#### Scenario: Live recount
- **WHEN** a live run's tail shows a refusal
- **THEN** the run's count SHALL be recounted at once rather than at the next 15 s recount

### Requirement: Pull request and artifact links
The task detail SHALL show the pull request of the task's latest exit (`GET /tasks/{id}/exits`) with its number, state, head and checks, and `o` SHALL open it in the browser using the forge's URL or one derived from the project's GitHub or GitLab repository.

#### Scenario: Open the pull request
- **WHEN** the task's latest exit has PR #12 and the owner presses `o`
- **THEN** the UI SHALL open the PR's URL with the system opener

### Requirement: Create task form
`n` on the board SHALL open a form for project, title, description (with `$EDITOR`), assignee, priority, dependencies and ready-now, validate fields before sending, and create the task with `POST /tasks` with a request id per submit; Core's refusal SHALL be shown under the form, which stays open.

#### Scenario: Ready task without an assignee
- **WHEN** the owner submits a ready-now task with no assignee
- **THEN** the form SHALL show the error at the assignee field and send nothing

#### Scenario: Core refuses
- **WHEN** Core refuses the create
- **THEN** the refusal SHALL be shown and the form's values SHALL be kept

### Requirement: Owner actions with confirmation
Every owner action except chat messages SHALL ask for confirmation before it is sent, text actions SHALL ask for the text first, and the kill switch state SHALL always be visible in the header.

#### Scenario: Pause cancelled
- **WHEN** the owner presses `p` on a task and then `esc`
- **THEN** nothing SHALL be sent

#### Scenario: Stop the company
- **WHEN** the owner chooses stop from the palette and confirms
- **THEN** the UI SHALL send the kill switch and the header SHALL show `■ STOPPED`

### Requirement: Notifications
A new owner attention row — a question or escalation that reached the owner, an approval request — SHALL show a toast and notify on the desktop (macOS notification, else the terminal bell), as chosen by `--notify=desktop|bell|off`. Each row SHALL notify once, at most one notification per 10 s with held-back rows counted into the next, and history replayed at startup SHALL NOT notify.

#### Scenario: Burst of approvals
- **WHEN** three approval requests arrive within 10 s
- **THEN** one notification SHALL be shown at once and the next SHALL say `(+2 more)`

#### Scenario: osascript fails
- **WHEN** the desktop notification command fails
- **THEN** the terminal bell SHALL ring instead

### Requirement: Demo, fixture and record modes
`--demo` SHALL run a built-in company and `--fixture PATH` a recorded one, both without Core or a token, with actions not sent and the footer saying so. `--record PATH` SHALL write Core's current state to a 0600 fixture file without run leases or the token.

#### Scenario: Action in a fixture
- **WHEN** the owner confirms an action in `--fixture` mode
- **THEN** nothing SHALL be sent and the footer SHALL say `fixture: not sent`

#### Scenario: Recording
- **WHEN** the owner records the state of a Core with live runs
- **THEN** the file SHALL contain the overview, feed, task details, run events and owner conversations, and SHALL contain neither the token nor any lease

### Requirement: Palette, mouse and resize
The UI SHALL offer a fuzzy command palette over tasks, agents, live runs, projects and actions, mouse clicks on tabs, chips, panels, rows and cards and wheel scrolling, and render every frame at exactly the terminal's size, following resizes; every screen SHALL fit 80×24 and below 60×16 the UI SHALL ask for a larger terminal. It SHALL pick a light or dark palette from the terminal's background.

#### Scenario: 80×24 terminal
- **WHEN** the terminal is 80×24
- **THEN** each screen SHALL render within 80 columns and 24 rows with the dashboard showing Agent Tree, Tasks and Activity

#### Scenario: Too small
- **WHEN** the terminal is resized to 50×12
- **THEN** the UI SHALL ask for a larger terminal instead of a broken layout
