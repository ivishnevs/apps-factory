# Development

Go 1.24.1; nats.go 1.41.2 (compatible with this Go baseline), modernc SQLite 1.36.3,
yaml.v3 3.0.1. SQLite is pure Go; no CGO toolchain is needed in the Core container.
Embedded migrations are internal/storage/schema.sql (version 1) migration2.sql (Task Engine) and migration3.sql (MCP audit), and migration4.sql (messaging).
Subsequent migrations add action approvals (5), artifact approvals (6), and reports/decisions (7).
Startup migrates transactionally to PRAGMA user_version = 7 without rewriting old run specs.
Back up a stopped Core data directory before upgrading; downgrade requires restoring its compatible backup.

`make build`, `make check`, `make up`, `make down`, `make schema`.
FACTORY_ROOT selects config root; FACTORY_DB selects SQLite file; FACTORY_LISTEN selects HTTP bind.
FACTORY_URL is the native CLI/Worker endpoint. NATS_URL is the authenticated broker URL.
CODEX_BIN selects the installed native Codex executable. FACTORY_TOKEN is required.
Core uses signal cancellation, HTTP shutdown deadlines and structured JSON logs.
Worker handles SIGTERM/SIGINT by requesting turn interruption before closing its process group.

Normal tests use fake RPC subprocesses, with no model/network. NATS_TEST_URL enables the integration
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
