Status: proposed 2026-11-11 by the owner ("put into proposals an MCP server or a skill that helps users with setup,
and later with ongoing support"). Partly implemented 2026-11-12: everything except 2.2 `factory import paperclip`
(see tasks.md).

Blockers: 2.2 needs the Paperclip export or API format; none is in the repository, the docs or this change, and
there is no recorded Paperclip API to test against, so no importer was invented. The `moving-in-from-paperclip`
runbook covers the move with Factory's commands until a real export is provided.

Decisions made in implementation:
- Confirmation is a code, not an MCP elicitation: a change tool called without `confirm` returns the effect and a
  single-use code (HMAC over the tool and its exact arguments, key per server process, valid 10 minutes); clients
  without elicitation support work the same way.
- `factory mcp` authenticates with an owner token (`FACTORY_TOKEN`; `FACTORY_COMPANY` sends `X-Factory-Company` for
  the operator token), not a browser session: a session cookie cannot be handed to a stdio process safely.
- The enrollment code is printed by `factory_node_enrollment`: it is a one-time, expiring code the owner needs on
  the new machine; every other secret stays out of answers, and node output passes through the credential redactor.
- Network checks: per runtime `net.dns` + `net.api` (Claude: api.anthropic.com or `ANTHROPIC_BASE_URL`'s host, none
  for Bedrock/Vertex; Codex: chatgpt.com), the forge as `net.forge` in doctor and the periodic self-checks (reported,
  not blocking). "Reach" is a TCP connection to 443, not a TLS or HTTP request. A lookup or dial timeout is transient
  (the existing retry-before-disable rule); "no such host" and a refused connection disable at once.
- A network fault needs both an errno text (ENOTFOUND, ECONNREFUSED, EAI_AGAIN, getaddrinfo, no such host,
  connection refused) and an API host in the run's result, and counts even after the agent used tokens (unlike other
  node faults); side-effect tasks are still parked for inspection. It is charged per project like other node faults
  (re-check after `ReadinessRecheck`), and the node probes its drivers at once.
- The company doctor is computed in Core (`GET /doctor`) so the CLI, the MCP server and Telegram say the same; the
  Telegram "digest" is the existing daily digest plus the doctor's problems, and `/doctor` on request.
- A pause's clearer said `factory resume project P`, which the CLI does not take; it now says
  `factory resume --project P` (`--quota-key K`).
- The company doctor's "stuck" excludes waits that clear by themselves (capacity, backoff, a claim on its way) and
  includes a session tie only when its holder node is inactive.

## Why

Setting up and keeping the tvorog company running on 2026-11-11 took a long chat with a coding agent that read
Core's code, logs and database by hand. Each fault was found by reading code, and none was named by the product:

- **Moving in:** the Paperclip company (projects, open issues, prompts, skills, secrets) was moved with a one-off
  script, and the stored catalog had to be fixed format error by format error until Core started.
- **Node setup:** the old node config had keys the node refuses (`company`, `token_file`). Enrolling, approving and
  starting the node took commands from four docs.
- **Second machine:** Core listens on loopback and a remote node needs https. Choosing between an SSH tunnel and
  `tailscale serve` took reading `internal/node/enroll.go`.
- **Broken DNS on a node:** corporate Cloudflare WARP (then a stale Tailscale resolver) broke name resolution on mac2.
  The node passed `doctor` and enrolled, yet every Claude run failed with `ENOTFOUND` for api.anthropic.com, and the
  task burnt two of its three attempts before anyone noticed.
- **Why nothing runs:** disk below the critical watermark, a project pause invisible in the web, a session pinned to
  a paused node, a node without a Claude login. The answers were in `GET /tasks/{id}/why`, `nodes list`, the node log
  and `df`; the owner saw "start or fix a node" or a silent chat.
- **Questions about the product:** which Claude config dir and account a node uses, which skills an agent really
  gets, what "350k tokens" on a run means.

A customer without a developer at hand would have stopped at the first of these. Factory knows its own state and
docs, so it can carry this knowledge itself.

## What Changes

- **Owner MCP server (`factory mcp`, stdio, the owner's token or session).** It is for the owner's own coding agent
  (Claude Code, Codex, Claude Desktop), not for company agents. It offers read tools over Core and the local node:
  - company status: projects, pauses with reason, nodes, disk, drivers and logins;
  - why a task or chat waits (`/tasks/{id}/why` in words);
  - recent runs with errors and usage explained;
  - node doctor results and node logs (local node only);
  - catalog validation with the fix for each error;
  - which skills and prompt an agent really gets.
  Change tools (resume/pause a project or node, issue an enrollment code, approve a fingerprint, set a secret, install
  a skill) show the exact effect and run only after the owner confirms in their agent. No tool prints a secret.
- **Skill `factory-setup` (source `factory`) with runbooks.** It guides the owner's agent through:
  - first company setup;
  - moving in from Paperclip, with an importer command (`factory import paperclip`) that replaces the one-off script;
  - adding a machine (same LAN, Tailscale `serve` with https, why not a plain LAN URL);
  - node logins (`factory-node login claude`, shared quota);
  - disk (what may be deleted, watermarks);
  - DNS and proxy faults (WARP, VPN, stale resolvers);
  - "agent does not answer";
  - pauses and token usage explained.
  Every runbook ends with a check that proves the fix, for example a node `doctor` pass or a run that starts.
- **Support mode for later.** The same MCP server and skill answer support questions from live state: a daily
  `factory doctor --company` summary (nodes, disk, failed runs by cause, stuck tasks, pending owner items), sent to the
  owner on request or to Telegram. Each item names the fix or the runbook.
- **Node checks catch what bit us.** `factory-node doctor` and the node's periodic checks resolve and reach the
  runtime's API host (api.anthropic.com for Claude, the Codex endpoint) and the forge (github.com) through the system
  resolver, the way runtimes do. A failure (`net.dns`, `net.api`) disables the driver with the cause and the fix, so
  runs stay queued instead of failing and burning attempts. A run that ends with a network error such as `ENOTFOUND`
  or `ECONNREFUSED` to the API host counts as a node fault: it is retried elsewhere and does not count as an attempt
  without progress.

## Impact

- New: `cmd/factory` `mcp` (owner MCP server over the owner API), `skills/factory-setup/` (built-in source `factory`),
  `factory import paperclip`, `factory doctor --company`.
- Node: `net.dns` / `net.api` checks in `doctor` and probes; network-class run failures as node faults.
- Core: read endpoints the MCP tools need where the owner API lacks them (run usage explained, effective agent skills
  and prompt).
- Docs: `docs/owner-ui.md` (owner MCP), `docs/node-install.md` (remote machines, DNS), new `docs/setup-assistant.md`.
- Related: `disk-pause-conversations` (the disk reason and conversations during a disk pause), `node-readiness`.
