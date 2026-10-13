## 1. Storage
- [ ] 1.1 Add Postgres to `compose.yaml` (volume, healthcheck, loopback port) and `FACTORY_DATABASE_URL`; remove `FACTORY_DB`. Verify: `make up` starts Postgres before Core; `factory doctor` reports Postgres.
- [ ] 1.2 Switch `internal/storage` to `pgx/v5/stdlib`; port placeholders, timestamps, `jsonb`, booleans. Verify: all existing `internal/storage` tests pass against a disposable Postgres (`POSTGRES_TEST_URL`).
- [ ] 1.3 Add embedded numbered migrations with `schema_migrations` and a startup advisory lock; `0001_baseline.sql` = schema v7 + tenancy. Verify: two Core processes starting together apply migrations once; a newer DB version makes Core refuse to start. Partial: SQLite: migrations and schema_migrations done (aeec9b3); Postgres advisory lock pending.
- [ ] 1.4 Port unique partial indexes with `company_id` first and keep `ErrConflict` mapping. Verify: duplicate dispatch and duplicate writer tests still return 409.

## 2. Tenancy
- [ ] 2.1 Add `accounts`, `users`, `companies`, `memberships`; `company_id` and composite keys/FKs on every table. Verify: a test inserting a row that references another company's row fails with a FK error. Partial: `companies` and `company_id` on every table done (6c302ff); accounts, users, memberships pending.
- [x] 2.2 Thread `companyID` through every `Store` method and `internal/core` handler; owner tokens resolve to a company. Verify: API test with two companies shows no cross-company reads, writes, feed items or inbox entries. Done: 6c302ff.
- [x] 2.3 Bind run credentials to the company; reject cross-company MCP calls. Verify: `mcp_test` case with a token of company A calling a task of company B returns 404. Done: 6c302ff.
- [ ] 2.4 Load the agent/policy/profile catalog per company (`configs/<company>/`, current tree = `default`). Verify: `factory config validate --company X` and an agent with the same name in two companies resolve independently.
- [ ] 2.5 Prefix NATS subjects and consumers with the company. Verify: JetStream integration test runs two companies' commands without cross-delivery.

## 3. Locking and outbox
- [ ] 3.1 Take `pg_advisory_xact_lock(company)` in schedule, claim, expiry, approval expiry and message scheduling transactions. Verify: parallel scheduler/claim/expiry stress test never produces two live runs for one task.
- [ ] 3.2 Outbox publisher with `FOR UPDATE SKIP LOCKED` and unchanged MsgId semantics. Verify: two publishers in parallel publish each row once; a crash before mark republishes the same ID.

## 4. Migration and docs
- [ ] 4.1 `factory admin import-sqlite` (read-only source, one transaction, active runs imported as UNKNOWN). Verify: import of a fixture v7 database round-trips counts, versions and sequences.
- [ ] 4.2 Update README, `docs/architecture.md`, `docs/development.md` (Postgres test URL, backups). Verify: `make check` passes with Postgres running.
