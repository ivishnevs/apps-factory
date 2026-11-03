## Why
The owner CLI (`waits-events-cost-cli`) answers one question per command: `status`, `log`, `show`, `answer`. Watching a company of agents at work needs one live picture instead — who is doing what right now, what each run is doing step by step, who passes what to whom, what waits on the owner — with the owner's actions in place. Without it the owner polls several commands, misses questions and approvals, cannot see a run's full runtime session or its tool errors, and leaves the terminal to open a pull request or read an artifact before approving it. The owner UI was built first on the `tui` branch; this change records what exists.

## What Changes
- **`factory ui`**: a full-screen terminal UI on Bubble Tea v2 (`internal/tui`, `cmd/factory/ui.go`), a client of the owner API with the owner token, keeping no state. Screens: 1 Dashboard (agent tree, tasks, stats, activity, pulse), 2 Board (Kanban columns, card modal with every task action), 3 Flow (swimlanes and pairs), 4 Feed, 5 Inbox (answers, approvals, artifact preview), 6 Chat (the owner's direct reports); a live run view and the full runtime transcript of any run; a prompt bar for the oldest item waiting on the owner, toasts, a command palette, mouse support, help.
- Tool error counts per run (badges on the tree, cards and run headers, with the last error); pull request and artifact links (`o`, `v`); a create-task form (`n`); desktop notifications for new owner questions, approvals and escalations (`--notify`).
- Every owner action asks for confirmation; the kill switch is always visible.
- `--demo` (built-in company), `--fixture PATH` (browse a recording) and `--record PATH` (snapshot Core's state without leases or the token) work without a live Core where they can.
- Core: `GET /runs/{id}/events/stream` — a run's events as server-sent events with `Last-Event-ID`/`?after=`/`?tail=N`, without the worker lease, closed by `event: end`; message feed rows summarise `sender → recipient (type): body`; `GET /tasks/{id}/exits` lists a task's accepted exits with their git and forge facts; `GET /health` names `default_report`.
- The Bubble Tea v2 modules raise the Go floor and the build image to Go 1.25.
- Owner backlog (6.x): why a run is QUEUED, 7 Nodes (drivers, disk, pauses), pauses from the UI, 8 Costs, Core's parks, rendering only on change, notifications that take no answer; Core: `queued` reasons, `GET /tasks/{id}/parks`, driver `transient_failures`/`until` and disk watermarks, `GET /costs?by=project`.

## Out of scope
A web dashboard, a UI configuration file (flags are the settings), UI-side persistence (unread markers are per session), `reopen` and `reset-workspace` owner endpoints, glamour-based markdown rendering.

## Dependencies
`waits-events-cost-cli` (owner API, feed SSE, costs, pending count, kill switch), `messaging` (inbox, conversations, `POST /messages`), `hierarchy` (`reports_to`, owner conversations, `owner.default_report`, task authority actions), `action-approvals` and `artifact-approvals` (approvals, artifact content), `explicit-exits-authority` (exits and their facts), `runtime-drivers` and `core-owned-project-data` (run events with the runtime's full stream, node redaction).

## Capabilities
### New Capabilities
- `owner-ui`: the owner's full-screen terminal UI over the owner API.

### Modified Capabilities
- `event-feed`: adds the per-run event stream over SSE.
- `messaging`: message feed rows name sender and recipient.
- `run-exits`: adds the owner's list of a task's accepted exits with facts.
- `agent-hierarchy`: `GET /health` names the owner's default report.
- `trigger-scheduling`: a QUEUED run says why no node claims it (`queued` on the run, in `/status` and the why).
- `runtime-drivers`: drivers report their transient failure count and auth hold-off end; node health its disk watermarks.
- `task-engine`: `GET /tasks/{id}/parks` lists a task's parks.
- `cost-ledger`: costs group by project.

## Impact
New `internal/tui` (client, fixture, views, transcript parsers, notifier, links, form, goldens), `cmd/factory/ui.go`; `internal/core` (`owner.go` run event SSE sharing the feed loop, `tasks.go` exits route, `core.go` health), `internal/storage` (`feed.go` message summaries, `storage.go` run events after a sequence, `exits.go` task exits); `go.mod` (charm.land bubbletea 2.0.9, bubbles 2.2.1, lipgloss 2.0.6; Go 1.25), `Dockerfile`, docs `owner-ui.md`, `development.md`, README; the CLI goldens whose message rows changed.
