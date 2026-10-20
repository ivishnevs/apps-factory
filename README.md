# App Factory

App Factory runs a company of AI agents that build software. A Go **Core** owns all state in **Postgres** and
dispatches work over **NATS JetStream** to **nodes** (`factory-node`), which run each agent as a **Codex** or
**Claude** session next to the code, Git and Xcode. The owner (you) steers it through a CLI, an inbox and Telegram.

- **Companies and catalogs.** One Core serves one company (`FACTORY_COMPANY`). Its catalog is `configs/` for the
  `default` company and `configs/<company>/` for any other (`agents/`, `profiles/`, `policies/`, `node-hooks/`,
  `skills.yaml`, `quota_keys.yaml`, `company.yaml`); `configs/prices.yaml` is shared. Unknown YAML keys are errors.
- **Hierarchy.** Agents form a tree through `reports_to`; agents without it report to the owner, and
  `owner.default_report` in `company.yaml` receives unaddressed owner messages (`@name ...` picks another).
- **Projects and tasks.** Core keeps projects, a task DAG (BACKLOG → READY → IN_PROGRESS → IN_REVIEW → DONE),
  attempts, reviews, conversations, reports, a versioned Decision Registry, artifacts and approvals.
- **Explicit exits.** Every agent run ends with a `factory_exit_*` call (`submit`, `handoff`, `done`, `ask`,
  `block`, `wait`) through Factory MCP; Core checks it against facts (clean tree, pushed branch, merged PR, secret
  scan). Waiting on the outside world is only a timer (`factory_exit_wait`).
- **Task worktrees and PRs.** With `worktrees.enabled`, each task gets its own git worktree on branch `af/<task>`;
  work is sealed after every run, the branch is pushed and a PR opened with `gh` on `submit`/`handoff`, and `done`
  of a repository task needs the PR merged. Tasks of one project run in parallel.
- **Policies and command guards.** One policy catalog covers access, escalation, reporting and recovery rules;
  every MCP call and shell command is checked (deny, allow or owner approval). These are guardrails, not a sandbox.
- **OpenSpec in Core.** Product specs and changes live in Core storage; nodes materialize them into the worktree
  and sync edits back at exits; tasks link to checked active changes.
- **Recovery, costs, Telegram.** Failed runs retry with backoff and escalate; every run gets a cost row
  (`configs/prices.yaml`); a Telegram bot sends what needs attention, reminders and a daily digest.

## Architecture

```
  owner: factory CLI / Telegram
            │ HTTP (FACTORY_TOKEN)
  ┌─────────▼─────────┐      ┌──────────────┐
  │ Core (Docker)     │─────▶│ Postgres 17  │  all state, per-company rows
  │ API, scheduler,   │      └──────────────┘
  │ outbox, blobs     │      ┌──────────────┐
  │                   │◀────▶│ NATS JS 2.11 │  commands, events, feed, node control
  └─────────▲─────────┘      └──────▲───────┘
            │ claim/lease/exits     │ pull consumers
  ┌─────────┴───────────────────────┴────────┐
  │ factory-node (macOS host, N slots)        │
  │  Codex driver ─ codex app-server          │
  │  Claude driver ─ factory-shim + Agent SDK │
  │  factory-mcp (Factory tools, exits)       │
  │  task worktrees, janitor, doctor          │
  └───────────────────────────────────────────┘
```

Core runs in Docker; nodes run on the machines that have the workspaces and toolchains. Core never reads node
disks: project data (OpenSpec, artifacts, logs) moves through the API. Details: [architecture](docs/architecture.md).

## Prerequisites

- Go 1.24.1+ (`go.mod`), Git.
- Docker with the `docker compose` plugin (Docker Desktop or Colima).
- Node.js 22+ and npm: `make build` builds the Claude shim in `workers/claude`.
- **Codex CLI 0.162.1** exactly, logged in with `codex login` (ChatGPT; no API key). Other versions are refused.
- For Claude agents: a Claude subscription login (`claude`, then `/login`). The shim uses
  `@anthropic-ai/claude-agent-sdk` (pinned in `workers/claude/package-lock.json`); API keys are refused by default.
  See [Claude runtime](docs/claude-runtime.md).
- `gh`, authenticated (`gh auth status`), for the push/PR flow of task worktrees.
- Python 3 for the smoke scripts.

## Quick start

```sh
cp .env.example .env           # only if .env does not exist yet
# Replace FACTORY_TOKEN, NATS_PASSWORD, POSTGRES_PASSWORD with random values, e.g. openssl rand -hex 32
chmod 600 .env
set -a; source .env; set +a
export FACTORY_URL=http://127.0.0.1:8080
export NATS_URL="nats://factory:${NATS_PASSWORD}@127.0.0.1:4222"

make build                     # bin/factory, factory-node, factory-mcp, factory-shim, codex-worker + Claude shim
make up                        # docker compose: postgres, nats, core
./bin/factory config validate  # --company C for another company's catalog
./bin/factory doctor           # Core, its Postgres and NATS
```

Start a node (relative paths resolve against the directory you start it in, so run it from the checkout):

```sh
mkdir -p data
cat > data/node.yaml <<'YAML'
state_dir: data/factory-node
slots: 2
drivers:
  codex: {enabled: true}
  claude: {enabled: true}      # off by default; needs Node 22+ and a Claude login
worktrees: {enabled: true}     # per-task git worktrees (projects with a repo)
YAML
./bin/factory-node doctor -config data/node.yaml   # git, gh, disk, drivers, Core, NATS
./bin/factory-node run -config data/node.yaml
```

The node also takes `FACTORY_NODE_TOKEN` (`factory nodes token create NAME`) instead of the shared token; see
[node process](docs/development.md#node-process) for every `node.yaml` key, outbound TLS/WebSocket NATS,
`factory-node launchd` / `systemd` units and `factory-node workspace reset`.

First project and task, in another terminal with the same environment:

```sh
mkdir -p .cache/my-project
./bin/factory projects create my-project "$PWD/.cache/my-project"   # add --repo URL [--branch main] for PRs
./bin/factory tasks create - <<'JSON'
{"id":"first-task","project_id":"my-project","title":"First task","description":"Reply TASK_ONE_OK; do not modify files.","assignee":"demo","request_id":"create-first-task"}
JSON
./bin/factory tasks ready first-task 1
./bin/factory watch my-project
./bin/factory show first-task
./bin/factory tasks review first-task VERSION accept   # once IN_REVIEW, VERSION from tasks get
```

The workspace is an absolute path on the node's machine, not inside a container. More: [task lifecycle](docs/task-lifecycle.md).

### Security notes

- Postgres, NATS and Core publish their ports on loopback (`127.0.0.1`) only.
- `.env` is excluded from Git and the Docker build context; keep it `0600`. Never print `.env`, database URLs with
  passwords or authenticated NATS URLs in reports or logs.
- `FACTORY_TOKEN` is the owner token; runs get scoped, company-bound run tokens; nodes should use per-node tokens
  (`factory nodes token create`). Token and creds files should be `0600` (`factory-node doctor` warns).
- Data lives in the volumes `postgres-data`, `nats-data`, `core-blobs` and `core-data`. `make down` keeps them;
  `docker compose down -v` deletes them. Backups: [development](docs/development.md).

## Owner CLI

`./bin/factory` with no arguments prints the full usage. The main groups:

```sh
./bin/factory status [PROJECT] | watch [PROJECT] [--attention] | log TASK | show TASK   # --json for scripts
./bin/factory pending                          # questions and escalations waiting for you
./bin/factory answer MSG OPTION|TEXT           # answer a question; approve|reject ID REASON for approvals
./bin/factory comment TASK TEXT [--urgent]     # owner input; --urgent stops the current run
./bin/factory direct TASK TEXT [--urgent]      # authority directive to the task's agent
./bin/factory conversations say PROJECT TEXT   # talk to a direct report (@name TEXT)
./bin/factory run|retry TASK                   # release a task now
./bin/factory pause|resume --all|--project P|--agent A|--quota-key K|--node N [REASON]
./bin/factory stop [REASON] | start            # company kill switch
./bin/factory costs [--task T|--agent A|--day|--model] [--since 24h]
./bin/factory rules company|PROJECT [TEXT|-]   # standing rules; time-zone [ZONE] for daily budgets
./bin/factory telegram bind                    # one-time code: send /start CODE to the bot
./bin/factory projects ... | tasks ... | runs ... | inbox ... | approvals ... | artifacts ... | openspec ...
./bin/factory reports ... | decisions ... | agents ... | skills ... | nodes ...
```

Telegram is enabled by `FACTORY_TELEGRAM_TOKEN` (plus `FACTORY_TELEGRAM_PROJECT`, `FACTORY_TELEGRAM_DIGEST_AT`) in
Core's environment (with `make up`, set them in `.env`; `compose.yaml` passes them to Core). Details: [owner controls](docs/task-lifecycle.md#owner-controls),
[messaging and Telegram](docs/messaging.md#telegram), [event feed and costs](docs/reports-decisions.md#event-feed-and-costs).

Owner TUI: see branch `tui` / [docs/owner-ui.md](docs/owner-ui.md) once merged.

## Development and tests

```sh
make hooks                     # once per clone: gofmt pre-commit hook
make test-services             # throwaway Postgres (127.0.0.1:15432) and NATS (127.0.0.1:14231) in tmpfs
export FACTORY_TEST_DATABASE_URL='postgres://postgres:factory@127.0.0.1:15432/postgres?sslmode=disable'
export NATS_TEST_URL=nats://127.0.0.1:14231
go test -p 1 -count=1 ./...    # -p 1: packages share the JetStream streams
make check                     # gofmt, go test -race, go vet, shim checks and tests
make test-services-down
```

The Makefile exports `FACTORY_TEST_DATABASE_URL` for `make test`/`make check`; without these variables the
storage and JetStream tests are skipped. Point them only at servers you can throw away: tests create and drop
databases and delete streams. Normal tests use fake runtimes, never a real model.

Secure NATS tests (TLS, WebSocket, nkeys, creds): `scripts/nats-secure-test.sh up` prints the `NATS_TEST_*`
exports for the gated tests in `internal/bus` and `internal/node`; `scripts/nats-secure-test.sh down` removes the
servers. More: [development](docs/development.md).

### Live smokes

Live smokes spend subscription usage. Run them against the cheap **smoke company** (`configs/smoke/`: only the agents
the smokes use, cheapest models, `effort: low`, no retries), never the production catalog:

```sh
FACTORY_COMPANY=smoke FACTORY_EXPLICIT_EXITS=1 docker compose up -d --build core
./bin/factory config validate --company smoke
# start a node with company: smoke in its node.yaml, then:
scripts/messaging-smoke.py --restart-core
scripts/openspec-smoke.py --restart-core
scripts/drivers-smoke.py
```

The scripts check `GET /health` and refuse to run against the wrong company. Full procedure:
[live smokes](docs/development.md#live-smokes-cheap).

## Documentation

- [adding-runtime.md](docs/adding-runtime.md) — how to add a new agent runtime driver.
- [agent-configuration.md](docs/agent-configuration.md) — agents, profiles, hierarchy, runtime and workspace settings.
- [agent-config-verification.md](docs/agent-config-verification.md) — Agent Config verification results.
- [approvals.md](docs/approvals.md) — owner approvals of exact MCP actions and shell commands.
- [approvals-verification.md](docs/approvals-verification.md) — action approval verification results.
- [architecture.md](docs/architecture.md) — Core, outbox, claims, leases, tenancy and NATS subjects.
- [artifact-approvals.md](docs/artifact-approvals.md) — versioned artifacts, uploads and approval-gated tasks.
- [artifact-approvals-verification.md](docs/artifact-approvals-verification.md) — artifact approval verification results.
- [calorie-calculator-e2e.md](docs/calorie-calculator-e2e.md) — test-project autonomous-team E2E log.
- [claude-runtime.md](docs/claude-runtime.md) — Claude Go driver, Agent SDK shim, login and limits.
- [decision-autonomy-verification.md](docs/decision-autonomy-verification.md) — delegated autonomy verification results.
- [development.md](docs/development.md) — storage, tests, node process, worktrees, NATS security, backups, smokes.
- [explicit-exits-contract.md](docs/explicit-exits-contract.md) — adapter ↔ Core contract for `factory_exit_*`.
- [factory-mcp.md](docs/factory-mcp.md) — Factory MCP server, run tokens and tools.
- [factory-mcp-verification.md](docs/factory-mcp-verification.md) — Factory MCP verification results.
- [foundation-verification.md](docs/foundation-verification.md) — Foundation verification results (historical).
- [messaging.md](docs/messaging.md) — messages, conversations, owner questions, inbox and Telegram.
- [messaging-verification.md](docs/messaging-verification.md) — messaging verification results.
- [openspec.md](docs/openspec.md) — OpenSpec documents stored in Core: API, CLI, MCP, node materialization.
- [openspec-verification.md](docs/openspec-verification.md) — OpenSpec integration verification results.
- [policies.md](docs/policies.md) — unified policies, command guards, wait and reporting rules.
- [reports-decisions.md](docs/reports-decisions.md) — reports, Decision Registry, autonomy, event feed and costs.
- [reports-decisions-verification.md](docs/reports-decisions-verification.md) — reports and decisions verification results.
- [runtime-protocol.md](docs/runtime-protocol.md) — Codex 0.162.1 app-server protocol and limits.
- [runtime-protocol-v2.md](docs/runtime-protocol-v2.md) — Core ↔ node protocol v2 (registration, heartbeat, control).
- [skills.md](docs/skills.md) — per-company skills registry and how runs get skills.
- [task-engine-verification.md](docs/task-engine-verification.md) — Task Engine verification results.
- [task-lifecycle.md](docs/task-lifecycle.md) — projects, tasks, scheduling, recovery, waits and owner controls.

## OpenSpec

Requirements and change history of App Factory itself are in [`openspec/`](openspec/) (`openspec/changes/`, one
directory per change). OpenSpec as a product feature for managed projects: [docs/openspec.md](docs/openspec.md).
