## Context
`internal/storage` opens SQLite with `SetMaxOpenConns(1)`, applies `schema.sql` + `migration2..7.sql` by `PRAGMA user_version`, and relies on the single connection for mutual exclusion (scheduling, claim, writer reservation). Entities are stored as JSON `body` blobs with a few indexed columns. Outbox rows are written in the mutation transaction and published by `Core.Run` after a JetStream ack.

## Goals / Non-Goals
Goals: Postgres as the only store; a tenant key on every row; explicit locking that replaces the implicit single-connection lock; same API/CLI/MCP behaviour for one default company.
Non-goals: multi-region, RLS, sharding, billing, per-company NATS accounts (subjects are prefixed now so accounts can be added later without renaming); importing an existing SQLite database (owner decision: no database is kept, SQLite is removed entirely).

## Decisions

### D1. Driver and access layer
Use `github.com/jackc/pgx/v5/stdlib` so `*sql.DB`, `*sql.Tx` and the `mutate[T]` / `getDoc[T]` helpers keep their shape. Placeholders change from `?` to `$n`; `strftime('%s','now')` becomes `extract(epoch from now())::bigint` or a Go-supplied timestamp (prefer Go time from an injectable clock, see `reliable-recovery`). JSON `body` columns become `jsonb`. Booleans become `boolean`.

### D2. Tenancy model
```
accounts(id, name, created_at)
users(id, account_id, email, created_at)
companies(id, account_id, name, created_at)                 -- account → many companies
memberships(company_id, user_id, role: owner|member)        -- user ↔ several companies
```
Every domain table gets `company_id NOT NULL REFERENCES companies(id)` and a composite key `(company_id, id)`; foreign keys are composite, so a row can never reference another company's row. Unique partial indexes are recreated with `company_id` as the first column (e.g. `active_task_run (company_id, task_id) WHERE status IN (...)`). Tables with a global sequence (`events.seq`, outbox) use `bigserial` plus `company_id`.

### D3. Company scope everywhere
- Owner/user tokens carry `company_id`; the owner HTTP API resolves it once per request (`core.companyFrom(r)`), and every `Store` method takes it as its first argument after `ctx`.
- Run credentials (`Core.runToken`) include `company_id` in the HMAC input; `agentGateway` rejects a token whose company differs from the run's.
- The agent catalog is loaded per company: `configs/<company>/{agents,profiles,policies}` with the current `configs/` treated as the `default` company. `config.Catalog` is cached per company.
- NATS subjects get a company token: `factory.<company>.commands.…` and `factory.<company>.events.…`. Streams keep one stream per kind with wildcard subjects; durable consumer names include the company only where a consumer is company-specific.

### D4. Locking that replaces the single connection
Scheduling, claim, expiry and any transaction that reads a snapshot and decides on it take `pg_advisory_xact_lock(hashtext(company_id))` first. Different companies never block each other. Ordinary reads use no lock. Unique partial indexes remain the last line of defence; a unique violation maps to `ErrConflict` exactly as today (`constraint()` in `tasks.go`).

### D5. Migrations
`internal/storage/migrations/NNNN_name.sql`, embedded with `embed.FS`, applied in order inside one transaction each and recorded in `schema_migrations(version, applied_at)`. Startup takes `pg_advisory_lock(<fixed key>)` so two Core processes cannot migrate concurrently. Core refuses to start on a database whose highest version is newer than it knows (as today with `user_version > 7`). `0001_baseline.sql` is the whole current schema on Postgres plus tenancy (owner decision: the SQLite migration history collapses into it, no database is kept; later changes are `0002_...`).

### D6. Outbox semantics kept
Outbox rows are inserted in the mutation transaction (unchanged). The publisher selects `... WHERE sent_at IS NULL ORDER BY seq LIMIT 100 FOR UPDATE SKIP LOCKED` in a short transaction, publishes with `nats.MsgId(id)`, and marks sent; a crash between publish and mark republishes the same message ID (deduplicated by JetStream within its window, and idempotent at consumers as today). Several Core replicas may run the publisher safely.

### D7. Import (dropped)
Dropped by owner decision: no SQLite database is kept, so there is nothing to import; SQLite (driver, `FACTORY_DB`, SQLite-specific code and the old migrations with their upgrade tests) is removed.

### Implementation notes
- Queries keep `?` placeholders; `storage.DB`/`storage.Tx` number them for pgx. Flags stay 0/1 `bigint` columns (Go booleans are passed as 0/1) instead of `boolean`, and entity documents are `json` (stored byte for byte, read with `->>`) rather than `jsonb`, so replayed responses and snapshots are unchanged.
- Row IDs stay globally unique (`UNIQUE(id)` beside the composite key), so every existing lookup by a Core-issued ID stays correct; per-company ID namespaces would need every such lookup scoped first.
- Every Store transaction takes the company lock (a superset of D4's list), which reproduces the single-connection serialization per company.
- Core serves one company (`FACTORY_COMPANY`); its catalog is loaded once at start, so no per-company cache is needed yet.

## Risks / Trade-offs
- Postgres adds a service to run locally → one more container in `compose.yaml`, healthcheck before Core starts.
- Behaviour that silently depended on the single SQLite connection may race → D4 lock on every decide-then-write transaction; concurrency tests run scheduler, claim and expiry in parallel against a real Postgres.
- Company prefix in NATS subjects changes subject names → workers and Core are upgraded together; the old streams are drained before switching.

## Amendments

- 2026-10-20: `factory serve` registers the served company (`FACTORY_COMPANY`) in the default account when it does not exist (`Store.EnsureCompany`; a concurrent create by another Core counts as success), so a Core for a new catalog such as `configs/smoke/` starts instead of exiting "company not found" (278c47e).
- 2026-10-20: the skills registry is per company like the rest of the catalog: `configs/skills.yaml` for `default`, `configs/<company>/skills.yaml` for another, SKILL.md paths absolute or relative to that catalog directory (they may lead outside the factory root); no fallback to `configs/skills.yaml`. Prices stay shared (a4ebf62; agent-config amendment).
