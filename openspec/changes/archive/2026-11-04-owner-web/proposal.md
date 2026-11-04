## Why
The owner watches and steers the company from a phone as often as from a terminal. The terminal UI (`owner-tui`) needs a terminal; a web UI reachable over the owner's Tailscale network works on any device. It is also the first piece of a future SaaS site where users sign in to their own companies, so it is built as a real web app, not a page embedded in the CLI.

## What Changes
- **`factory web`** (`cmd/factory/web.go`, `internal/web`): a JSON API over the same owner client as the terminal UI (`tui.Client`): overview, task detail, runs with events and tool calls, feed, conversations, costs, artifact content, owner actions (`POST /api/action`, requires `X-Factory-Web: 1`), and server-sent event streams of the feed and a run. It holds the owner token; the browser never sees it. Listens on 127.0.0.1:8090 by default; `--demo`/`--fixture` serve a recorded company without Core.
- **`web/`**: a Next.js (App Router) + TypeScript + Tailwind + shadcn/ui app, mobile-first with a desktop sidebar: Home, Tasks (board and detail with every owner action), Run (live transcript), Inbox (questions, approvals, escalations), Activity (live feed), Chat (the owner's direct reports), Costs, Nodes (and pauses, kill switch). Next proxies `/api/*` to `factory web` (`FACTORY_WEB_API`). Every owner action asks for confirmation.
- Served to the owner's devices over Tailscale (`next start -H <tailscale IP>`), never on a public address.

## Out of scope
Sign-in, multiple companies and users (the SaaS step: Next adds the auth and marketing route groups, the API becomes multi-tenant), a public deployment, push notifications.

## Dependencies
`owner-tui` (the owner client, actions, fixtures), `waits-events-cost-cli` (owner API, feed SSE, costs), `messaging`, `action-approvals`, `artifact-approvals`.

## Capabilities
### New Capabilities
- `owner-web`: the owner's web UI and its API.
