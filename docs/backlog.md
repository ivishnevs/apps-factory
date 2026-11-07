# Backlog

## Known issues

- **The web UI has its own API layer in front of Core's API (`internal/web`), architectural, recorded
  2026-11-07, not planned.**
  - *How it came about.* The web started as `factory web`, a local program on the owner's Mac that held the owner
    token and called a remote Core. A backend-for-frontend kept the token out of the browser, merged several Core
    calls into one (`/api/overview`, task detail) and served a demo mode from fixtures. With SaaS (saas-web D1) Core
    serves the web itself and signs people in by session, but the layer was moved into Core's process
    (`cmd/factory/web.go`, `web.HandlerTransport`) instead of removed.
  - *Why it is a problem.* Core now has two APIs: `/api/*` for the browser (about 20 routes in
    `internal/web/server.go`) and its own API for the CLI, agents and nodes. The layer keeps its own allowlist of Core
    paths (`catalogPath` in `internal/tui/client.go`), which drifts. On 2026-11-07 `/decisions` was missing from it,
    so every decision card in the Inbox and the chat rendered empty, while Core served the decision fine (fixed in
    `9c0051e`). Each request is also serialised twice, and `/api/overview` makes about ten in-process HTTP calls every
    5 s per open tab.
  - *Target.* One API: browser, CLI, agents (MCP) and nodes call Core's HTTP routes, which differ only in how they
    authenticate (session cookie and CSRF, or a token). Then a service layer, then storage. The aggregates the layer
    builds (overview, task detail, catalog reads, `describe`) become Core endpoints that read in one query. Demo mode
    moves to a fake Core or frontend fixtures outside the production binary.
  - *Work when picked up.* Point the frontend at Core's routes, move the aggregates into Core, delete `internal/web`
    and the allowlist. It touches the whole frontend: give it its own OpenSpec change.

The NATS company-isolation issue (a node's NATS login could damage other companies' streams, and a
revoked node kept its login) is closed by `http-node-transport`: NATS is removed and nodes reach Core only through
signed HTTPS requests, checked per company and per node.

## Optional work

Optional work moved out of finished OpenSpec changes. Nothing here is planned; give an item its own
OpenSpec change when it is picked up.

## Terminal UI (from `owner-tui`)

- 6.7 Feed start without full history replay: start from a recent window and page back on demand. Verify: client test with a large feed.
- 6.8 Connection profiles: several Core URLs / companies with a switcher instead of one env file. Verify: config test.
- 6.9 Form for multi-field questions (options per field, free text). Verify: inbox test; golden.
- 6.10 Help overlay that scrolls on terminals under 30 rows. Verify: golden at 80x24.
- 6.11 Live CI status of a task's PR (not only the checks at submission). Verify: client test with fake forge data.
- 6.12 glamour markdown once the build image moves past Go 1.25.8. Verify: artifact preview golden.

## Company self-service (from `company-self-service`)

- 6.2 Terminal UI: read-only view of the catalog (optional).
