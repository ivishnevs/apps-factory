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

- **Project readiness does not check the project's toolchain, recorded 2026-11-13, not planned.**
  - *What happened.* In tvorog a reviewer run of advent-gift (a Go backend next to the iOS app) went to node mac2,
    which had no Go. The reviewer approved TVO-647 without building or testing ("there is no `go` in the review
    environment").
  - *Why it is a problem.* A node is "ready for a project" when it can read the repository; nobody checks the tools
    the project needs. Core routes by node labels (`macos`, `xcode`, …) and readiness, so it sends work to a node that
    cannot build or test it, and nothing tells people which tool to install.
  - *Direction.* The readiness check detects what the repository needs (`go.mod` → Go, `package.json` → Node,
    `*.xcodeproj` → Xcode, and so on for common ecosystems), with an optional explicit list in the project for unusual
    cases; a node missing a tool is not ready for that project, with the reason ("no go"), so work goes to another
    node and the node's status says what to install. Give it its own OpenSpec change when picked up.
- **A reviewer can approve without re-running the submission's checks, recorded 2026-11-13, not planned.**
  - *What happened.* The same review accepted `go build` and `go test` "from the implementer's report".
  - *Owner's decision on the rule (2026-11-13).* Accepting a check from the report is allowed, but the verdict must
    say so per check with the reason, and people see it on the task. Not re-running because the environment lacks a
    tool is covered by the readiness issue above (the review should run on a node that can).
  - *Relation.* `approval-gates-merge` §4 makes an `approved` verdict cover every submitted artifact; checks
    (build, tests) are not covered there yet.

- **A task waits for "a message from the owner" when it waits for a decision, recorded 2026-11-13, not planned.**
  - *What happened.* In tvorog the product manager's task 553cc263 (stashy prototype v4) waited for the owner's
    answer to the CEO's decision proposal `ac3f2bc2`, but the agent exited `wait` with `from: owner` instead of
    `decision_id`. The board showed "Waits for a message from you · Write to its agent", sending the owner to write to
    the agent instead of deciding the proposal in the Inbox.
  - *Why it is a problem.* The wait kinds exist (`decision_id`, `from`), but nothing ties a wait to the open decision
    or question it is really about, so the board points people to the wrong action and the task may wake on an
    unrelated message.
  - *Direction.* When a task waits on a person while a decision proposal or question it raised (or its manager raised
    for it) is open, Core links the wait to that item, or refuses `from` with a hint to wait on the decision; the
    board shows "Waits for your decision" with a link to it.
- **"duplicate?" flags a parent and its own subtask, recorded 2026-11-13, not planned.**
  - *What happened.* The same task 553cc263 ("prototype v4 — add kangaroo states, onboarding and chat") was flagged
    "duplicate?" against d0daebb9 ("prototype v4 — onboarding, AI chat and all kangaroo states"), the designer's
    subtask it created.
  - *Why it is a problem.* A coordination task and the subtask it delegates naturally share their title, so the
    similarity check raises false alarms people learn to ignore.
  - *Direction.* The duplicate check skips pairs linked by parent/child (and by origin task), and flags only tasks
    with no such link.

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
