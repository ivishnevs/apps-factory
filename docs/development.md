# Development

Go 1.24.1; nats.go 1.41.2 (compatible with this Go baseline), modernc SQLite 1.36.3,
yaml.v3 3.0.1. SQLite is pure Go; no CGO toolchain is needed in the Core container.
Embedded migrations are internal/storage/migrations/NNNN_name.sql, applied in order, each in its own
transaction, and recorded in schema_migrations; add a migration as the next numbered file. A database
from before schema_migrations is adopted at its PRAGMA user_version, which stays equal to the latest
version so older binaries refuse a newer schema. Core refuses to start on a version it does not know.
Back up a stopped Core data directory before upgrading; downgrade requires restoring its compatible backup.

`make build`, `make check`, `make up`, `make down`, `make schema`.
FACTORY_ROOT selects config root; FACTORY_DB selects SQLite file; FACTORY_LISTEN selects HTTP bind.
FACTORY_URL is the native CLI/Worker endpoint. NATS_URL is the authenticated broker URL.
CODEX_BIN selects the installed native Codex executable. FACTORY_TOKEN is required.
FACTORY_WORKER_STATE (default data/codex-worker) keeps pidfiles of running runtimes; on start the
Codex Worker kills process groups left there by its previous runs. Do not share it between Workers.
Core uses signal cancellation, HTTP shutdown deadlines and structured JSON logs.
Worker handles SIGTERM/SIGINT by requesting turn interruption before closing its process group.

Normal tests use fake RPC subprocesses or the internal/driver/fake scripted driver, with no model/network. NATS_TEST_URL enables the integration
test on an isolated broker; the test deletes its streams. scripts/smoke.py is a separately invoked
real ChatGPT smoke test and consumes subscription usage. It verifies start, streaming, completion
and thread resume, and prints only run metadata and requested final text.

Logs: `docker compose logs core`, `docker compose logs nats`; Worker logs to stderr.
Do not print .env or authenticated NATS URLs in reports. Back up both SQLite and the broker volume.
`docker compose down -v` deletes durable state and is not the normal stop command.

Foundation limitations: no automatic worker registration/heartbeat dashboard, no remote machine
path mapping, no event pagination/retention policy, no approval grants yet. Factory MCP enforces run-scoped Core access. Skill operations enforce the shared policy evaluator; shell access is trusted.
Do not proceed to later stages until Foundation checks pass.

Task Engine adds scripts/task-smoke.py (opt-in real model chain; --restart-core tests persistence),
SQLite migration/DAG/version/idempotency/review/retry tests and a JetStream fake-runtime restart test.
See task-lifecycle.md for the supported API/CLI and retry constraints.

Factory MCP builds a native sibling factory-mcp executable. See factory-mcp.md for scoped auth,
stdio tools, audit and the opt-in scripts/mcp-smoke.py planner/executor/reviewer test.

Messaging adds scripts/messaging-smoke.py --restart-core, blocking-answer races, communication scope,
form/duplicate/deadline/escalation/session tests. See messaging.md for HTTP/CLI and MCP contracts.

Action approval verification: `make check` covers exact grants, persistence, denial, expiry, revocation and writer quarantine. `scripts/approvals-smoke.py --restart-core` uses real Codex for approval and rejection with task/thread continuation. See [approvals](approvals.md).

Versioned artifact verification: `scripts/artifact-approvals-smoke.py --restart-core` exercises real Codex registration/submission, independent task review, v1 approval, v2 rejection and a v3 revision unblocking QA. See [artifact approvals](artifact-approvals.md).

Reports and decisions: `scripts/journal-smoke.py --restart-core` verifies three real Codex tasks, pending proposal persistence, independent inbox/task review, explicit replacement and captured run contexts. See [reports and decisions](reports-decisions.md).

OpenSpec uses Go 1.24 os.Root for confined file access, with no new dependency or migration (SQLite remains v7). `scripts/openspec-smoke.py --restart-core` exercises the native official CLI plus the Docker Core gateway and linked tasks. See [OpenSpec](openspec.md).

## Node doctor

`./bin/factory-node doctor` runs the node self-checks locally (node-agent D7) and needs no Core or NATS. It prints a
STATUS/CHECK/DETAIL table with a remediation hint under every `fail`/`warn` row and exits 1 when any check fails.
Checks: `git` and `git.user`; `forge.auth` (`gh auth status`, warn only) and, with `-repo URL`, `forge.repo`
(`git ls-remote`); `disk` free space at `-disk-path` (default `$HOME`) against `-low-gib 20` (warn) and
`-critical-gib 5` (fail); on macOS `xcode` (`xcode-select -p`, `xcodebuild -version`) and `xcode.simctl` (available
simulators; warn when none); `factory-mcp` in `FACTORY_BIN_DIR`, next to the binary, or `./bin`.
Runtime probes (`node.RuntimeProbe`, binary/version/auth, never a model call): `codex.*` (`CODEX_BIN`, exactly
`codex-cli 0.162.1`, `codex login status`) and `claude.*` (Node 22+, built `workers/claude`, `factory-workspace`, and
`claude auth status` of `CLAUDE_CODE_EXECUTABLE` or the `claude` CLI; warn when no CLI is installed, since the
SDK-bundled Claude Code is checked only at run start). By default only installed drivers are probed;
`-drivers codex,claude` requires the listed ones. `drivers` fails when no driver is usable.
Core/NATS reachability, stack hooks from `NodeHooks` and `node.yaml` watermarks arrive with the node process.
