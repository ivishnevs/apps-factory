## Context
After `waits-events-cost-cli`, `hierarchy`, `messaging`, the approval changes and `explicit-exits-authority`, the owner API exposes everything an owner needs — overview reads (`/health`, `/status`, `/tasks`, `/pauses`, `/pending/count`, `/nodes`, `/inbox`, `/approvals`, `/costs`), the feed SSE stream, run events and tool calls, conversations and every owner action — and the CLI renders it one command at a time. Core keeps each runtime's whole event stream as run events (Claude stream-json messages, Codex app-server notifications), redacted by the node (`core-owned-project-data` D6). The UI was built on the `tui` branch without OpenSpec; this design records it as built.

## Goals / Non-Goals
Goals: one live, full-screen picture of the company for the owner; every owner action reachable in place and confirmed; a run's full runtime session and its tool errors one key away; usable without Core (demo, fixture) for development and review; no secrets on screen, in URLs or in recordings.
Non-goals: a web UI, UI-side state or config files, new owner actions beyond what the API offers, replacing the CLI (scripts keep `--json`).

## Decisions

### D1. Client layer apart from the views
`internal/tui` has a `Client` interface — `Overview`, `TaskDetail`, `Run`, `ToolCalls`, `Feed`, `StreamFeed`, `StreamRun`, `Do(Action)`, `RunEvents`, `Messages`, `ArtifactContent` — with two implementations: `HTTPClient` (owner token, `FACTORY_URL`/`FACTORY_TOKEN`, the token only in the `Authorization` header) and a fixture client (demo or recorded state; actions are not sent and the footer says `fixture: not sent — …`). The views (dashboard, board, flow, feed, inbox, chat, run, transcript, form, palette, help) are a Bubble Tea model over plain data and never touch HTTP; this keeps the views testable with the fixture and goldens, and the client testable against an `httptest` fake Core. `cmd/factory/ui.go` parses the flags and picks the client.

### D2. Bubble Tea v2
Bubble Tea 2.0.9, Bubbles 2.2.1 (textinput, textarea, viewport, spinner) and Lip Gloss 2.0.6 (`charm.land/.../v2` modules). They need Go 1.25, so `go.mod`, the build image and `docs/development.md` move to 1.25. v2 brings the cell-based renderer, mouse and resize messages and `tea.RequestBackgroundColor`, which picks the light or dark palette; colors are downsampled to the terminal's profile. Trade-off: a newer Go floor for everything in the repository. glamour v2 would need Go 1.25.8 while the image pins 1.25.0, so artifact previews use a small own markdown renderer.

### D3. Data sources and refresh
- **Overview** every 3 s, one read at a time; the agent catalog once. Only `/health` and `/status` failing marks Core unreachable (header dot and data age); other partial failures keep the last data.
- **Feed** through `GET /feed/stream` (last 3000 rows kept), deduplicated by `seq`.
- **Runs** through the new `GET /runs/{id}/events/stream`: each live run's last 20 steps then live (the activity stream and the tree's current step), and the opened run in full. Both streams share Core's SSE loop: `id` = sequence, resume with `Last-Event-ID` or `?after=`, `?tail=N` starts at the last N, the worker lease is left out, and an ended run closes with `event: end` `{status, outcome}`. The client reconnects with `Last-Event-ID` (1 s backoff doubling to 15 s), delivers each event once, and does not retry 400/401/404.
- **Flow** is derived from the feed: message rows now summarise `sender → recipient (type): body`, so a step's parties are known without fetching the message.
- **Task detail** adds `GET /tasks/{id}/exits` (accepted exits with node git and forge facts) for the pull request; the chat reads `default_report` from `/health`.

### D4. Transcripts: Core events first, local file as fallback
The transcript view is built from the run's stored events (`GET /runs/{id}/events`) plus Core's verdicts on Factory MCP calls (`GET /runs/{id}/tool-calls`), live-tailed by the run stream (`core events` badge). Parsers cover Claude (`claude/assistant`, `claude/user`: text, thinking, `tool_use`, `tool_result` with `is_error`) and Codex (`item/started`, `item/completed`: messages, reasoning, command executions with output and exit code, MCP calls, file changes). On the node's machine the UI also finds the runtime's own session file — Claude `$CLAUDE_CONFIG_DIR/projects/*/<session_id>.jsonl`, Codex `$CODEX_HOME/sessions/**/rollout-…-<thread id>.jsonl` (`--claude-config-dir`, `--codex-home`) — and `l` switches to it (used alone when Core has no events). A local file is read through the node's redaction rules (credential-named environment values, URL passwords, token shapes → `[REDACTED]`); side chains and the brief prompt are left out. Trade-off: two parsers per runtime format to maintain, in exchange for the session being visible even when Core lost the events.

### D5. Tool error counts
Per run, from its transcript: tool results with `is_error`, command guard refusals (exit 126; the refusal and the refused command's failed result are one incident), exit guard refusals, and Core refusals of Factory calls (tool calls with status ≥ 400; the runtime's failed result of that call is the same incident). Shown as `✗N` on tree rows, cards, run and transcript headers, with the last error; live runs recounted every 15 s and at once on a refusal in their tail; ended runs counted when a task detail lists them.

### D6. Owner actions and confirmation
Every action — task pause, resume, retry, run, cancel (cascading), reassign, priority, comment, directive (`!text` urgent), answers, approvals, kill switch stop/start — asks first (`y`/`enter` sends, `n`/`esc` cancels); text actions ask for the text, then confirm. Chat messages are the exception (a conversation): `enter` sends through `POST /messages`, with `@name` addressing checked against the owner's direct reports before sending. The create-task form validates locally (title, a ready task needs an assignee, priority −1000..1000) and sends `POST /tasks` with a `request_id` per submit; Core's refusal stays under the open form.

### D7. Demo, fixture and record
`--demo` is a built-in company with neutral names; `--fixture PATH` browses a recording; `--record PATH` writes Core's state (overview, last 2000 feed rows, detail of open tasks and up to 50 closed ones, events and tool calls of live runs and latest attempts, owner conversations) to a 0600 file without run leases or the token. Run views replay recorded events. Fixtures also drive the goldens.

### D8. Notifications, toasts, palette, mouse, layout
A new owner attention row (question, escalation, approval request) shows a toast and, by default, a macOS notification through `osascript` (title by kind, project as subtitle); elsewhere or on failure the terminal bell; `--notify=desktop|bell|off`. Each row notifies once, at most one per 10 s with the held-back count in the next, and history replayed at startup does not notify. The command palette (`ctrl+k`, `:`) fuzzy-searches tasks, agents, live runs, projects and actions. The mouse clicks tabs, chips, panels, rows and cards (second click opens), the wheel scrolls. Every frame is exactly the terminal's size; 100×30 is the design size, 80×24 fits every screen, below 60×16 the UI asks for a larger terminal; below 110 columns the dashboard drops Stats and Pulse.

### D9. Context gauge from the last model call
A run's usage rows are cumulative over every turn, so input + cache read divided by a window measured the run, not the context (705% on a long Claude run). The gauge reads the run's event stream instead: the dashboard keeps the context of every tailed live run, the run view derives it from its events. Claude: the latest top-level `claude/assistant` message's `usage` (input + cache read + cache creation; subagent messages, with `parent_tool_use_id`, are another context); the window from `claude/result*` `modelUsage[model].contextWindow` when the result has arrived. Codex: `thread/tokenUsage/updated` `tokenUsage.last.inputTokens` (includes cached) and `modelContextWindow`. Without a reported window a table applies: Claude 1M from the 4.6 generation (200k for Haiku 4.5 and older), gpt-6.1 500k, gpt-6 and gpt-5 272k (Codex models cache); an unknown model shows tokens only, a run without per-call usage `n/a`, over 100% is clamped and red. Cumulative rows stay, marked `(run total)`.

### D10. Why a run is queued: Core judges, the UI shows
The cause of a QUEUED run is computed by Core, not guessed by the UI: `storage.QueuedWhy` runs the v2 claim's checks (`ClaimV2`) without claiming — pauses with their reasons, the agent's caps, the pinned node, then every registered node: online, owner pause, critical disk, the provider's driver (its check, detail and auth hold-off `until`), labels, slots, resource classes — and reports `queued` `{reason, detail, until, nodes}` where the owner already reads: each QUEUED run in `GET /status` (one read for all of them per refresh), `GET /runs/{id}` and the task's why. The node closest to running the run gives the reason (`no_slot` < `resource_busy` < `node_paused` < `driver_disabled` < `node_offline` < `labels`): what will clear first. To report `until` and the transient count, the node's driver infos gain `transient_failures` and `until`, and its health the disk watermarks; they pass through the stored drivers and health JSON. Trade-off: the checks exist twice (claim and explanation); the test drives each cause through the real claim path.

### D11. Nodes, pauses and costs screens
Tab 7 shows `GET /nodes` (drivers, health, `in_use`) with `GET /pauses` and the feed's node events; pausing goes through the existing owner endpoints (`POST /pauses`, `POST /pauses/resume`, `POST /stop`), always scope → target → reason → confirmation. Tab 8 reads `GET /costs` once per grouping (day, agent, model and the new project) for the selected range when opened, on `r` and every 30 s while open. Parks in the task detail come from the new `GET /tasks/{id}/parks`; the feed-event rebuild remains only for a Core without the route. The tab line degrades from full names to short names to numbers with the current tab named, so 8 tabs and the status fit 80 columns.

### D12. Render only on change
Bubble Tea redraws after each message, so frames are what costs CPU. The spinner tick, which drives spinners, pulsing dots and timers at ~10 fps, is scheduled only while `wantsAnim` holds (loading, a toast sliding in, a send in flight, a running run on a screen that shows it, a chat reply); otherwise the pending tick is dropped and none is started until something live becomes visible. The data refresh (3 s) and the streams stay; the header clock shows minutes so an idle screen has nothing to redraw between refreshes.

### D13. Notifications in the inbox
Only `message_type: question` (and approvals) take an answer; a notification or report from an agent would be refused by `POST /inbox/{id}/answer` (409). The prompt bar and inbox render by type: a notification is `<agent> says`, replied to in the chat with that direct report (or with a directive on the task of another agent) and marked read with `POST /inbox/{id}/read`. A bare 409 is translated per action into what changed.

## Risks / Trade-offs
- Polling every 3 s plus one stream per live run → reads are sequential and streams resume rather than replay; acceptable for one owner.
- Feed-derived flow depends on summary text → the summary format is specified (messaging delta) and tested.
- Local transcripts may hold secrets → read only with the node's redaction rules; recordings never contain the token or leases.
- No UI persistence → unread markers and dismissed toasts reset per session; the database stays the record.

## Known gaps
- No UI config file; flags are the only settings.
- Desktop notifications are macOS only (`osascript`); other systems get the bell.
- Local transcripts exist only on the node's machine (`local: not available here` elsewhere).
- Artifact markdown uses an own renderer, not glamour, until the build image moves past Go 1.25.8.
- `reopen` and `reset-workspace` have no owner endpoint, so the UI does not offer them.
- Unread chat markers count from the owner's last message and this session; they are not stored.
- Not shown yet (tasks 6.7–6.12): feed start without full replay, connection profiles, multi-field question forms, scrolling help (the 100×30 help is cut since the Nodes and Costs keys were added), live CI status, glamour.
- Quota keys are typed when pausing: the UI has no list of the agents' quota keys.
- The queued reason is computed at read time; a run claimed between the read and the next refresh shows the old reason for up to one refresh.
