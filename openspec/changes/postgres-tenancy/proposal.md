## Why
SQLite with one open connection serializes every Core operation and cannot be shared by several Core processes or moved to a hosted database. The factory also has no tenant boundary: projects, agents, policies and runs are global, so a second customer, or a second company of the same owner, would share everything. Every later reliability change (trigger log, reconciler, hierarchy, cost ledger) adds tables that need a tenant key from day one.

## What Changes
- Replace SQLite (`modernc.org/sqlite`) with Postgres via `pgx` behind the existing `database/sql` store, keeping the transactional outbox, idempotent mutation replay, optimistic versions and unique partial indexes.
- Add the tenancy model: **account → companies → projects**. An account holds users (and later billing); a company holds its own agents, projects, policies, quota keys and nodes. A user can be a member of several companies of the same account.
- Add `company_id` to every table, composite primary/foreign keys `(company_id, id)`, and company scope on every query, API token and NATS subject.
- Replace the inline `PRAGMA user_version` chain with numbered, embedded, forward-only SQL migrations recorded in `schema_migrations`.
- No importer: no SQLite database is kept (owner decision); the migration history collapses into one Postgres baseline.
- Add Postgres to `compose.yaml`; Core keeps no local database file.

## Out of scope
Billing, sign-up, per-company NATS accounts and leaf nodes, row-level security, hosted deployment. Agent/policy YAML stays file-based (loaded per company directory).

## Dependencies
None. This is the first change; all later changes assume Postgres and `company_id`.

## Capabilities
### New Capabilities
- `postgres-tenancy`: Postgres storage, migrations and account/company tenancy for all Core state.

### Modified Capabilities
None.

## Impact
`internal/storage` (all files, migrations), `internal/core` (scoping, auth), `internal/config` (catalog per company), `internal/bus` and `workers/claude/src/bus.ts` (subject prefix), `cmd/factory` (company flag), `compose.yaml`, `Makefile`, `go.mod`, docs. Every existing behaviour stays the same for a single default company.
