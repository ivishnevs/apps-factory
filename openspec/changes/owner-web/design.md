## Context
`internal/tui` already has the whole owner client: `tui.Client` (HTTP against Core, or a fixture) with aggregated reads (`Overview`, `TaskDetail`, `Costs`), streams and `Do(Action)`. The web UI reuses it instead of a second client.

## Decisions
### D1. Two processes: a Go API holding the token, a Next.js app for the browser
`factory web` exposes `tui.Client` as JSON under `/api/` and is the only holder of the owner token. The Next.js app proxies `/api/*` to it (rewrites), so the browser talks to one origin and never sees a token. For the SaaS, Next takes over sessions (sign-in) and the API becomes multi-tenant; the browser contract stays `/api/*`.

### D2. API contract
`GET /api/meta`, `/api/overview` (tui.Overview), `/api/tasks/{id}` (tui.TaskDetail), `/api/runs/{id}`, `/api/runs/{id}/events`, `/api/runs/{id}/toolcalls`, `/api/feed?project&task&subtree&attention&after&limit` (or `before=N&limit=L` for the newest rows below N, oldest first; `before=0` is the newest), `/api/messages?conversation`, `/api/costs?since=24h`, `/api/artifacts/{project}/{artifact}/{version}?name&download` (raw, sandboxed); task materials: `/api/tasks/{id}/materials` (everything the task produced: report attachments with their report, artifact versions with their approval, submission artifacts, pull requests from the exits; newest first), `/api/materials?project&kind&q&before&limit` (the Library: the same items across the company, newest first; `kind` a comma list of document, pdf, web, image, data, code, file, link, pull_request; `before` Unix seconds, answered with `next_before` and `has_more`), `/api/blobs/{sha256}?name&download` (a report attachment raw, typed by its file name, with `Content-Security-Policy: sandbox`); `POST /api/action` with a `tui.Action` (Go field names) and the header `X-Factory-Web: 1` (a cross-site form cannot set it); SSE `GET /api/stream/feed`, `/api/stream/run/{id}` with events `feed`, `run`, `end`, `state`. Errors are `{"error"}` with 400 (bad input) or 502 (Core refused or failed). TypeScript mirrors live in `web/src/lib/api/types.ts`.

### D3. Stack
Next.js App Router, TypeScript, Tailwind v4, shadcn/ui (Radix), TanStack Query (polling 5 s, invalidation after actions), lucide icons, next-themes (system/light/dark). Screens are client components under `src/app/(app)/`; `(marketing)` and `(auth)` groups come with the SaaS.

### D4. Reach
`factory web` listens on 127.0.0.1; `next start` listens on the machine's Tailscale address, so only the owner's tailnet devices reach it. No public address.
