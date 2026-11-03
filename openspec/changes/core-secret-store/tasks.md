## 1. Store
- [ ] 1.1 `internal/secrets`: `KeyProvider` (env master key `FACTORY_SECRETS_KEY`), company keys wrapped by the master key, AES-256-GCM values with AAD company|name|version; `company_keys`, `company_secrets` tables. Verify: round-trip, wrong master key refused at start, ciphertext moved to another company/name fails, company deletion makes values unreadable.
- [ ] 1.2 Owner routes `PUT/GET/DELETE /secrets` (string and file kinds, 64 KiB cap, `used_by`), CLI `factory secrets set|list|delete` (value from stdin or file only), `factory secrets rotate-master` (operator). Verify: no route or CLI output carries a value; owner of company A cannot see or touch B's secrets; members see names only and cannot set or delete.
- [ ] 1.3 Owner web Secrets page of the company (`/c/{company}/secrets`; list, set/replace with masked field or file upload, delete); MCP server and agent views show needed secrets and whether they are set.

## 2. Agent secrets
- [ ] 2.1 `spec.secrets` (`{name, env}` / `{name, env, file: true}`, merged from profiles; reserved `FACTORY_*` env refused); `factory agents check` reports needed secrets that are not set. Verify: config tests.

- [ ] 2.2 Wait before dispatch (D9): the scheduler holds a task whose run needs a missing secret with `secret_missing: NAME (agent)`, no run and no retry spent; setting the secret wakes it; the reason is shown on the task and in the setup status. Verify: scheduler tests for waiting, waking and no retry counted.

## 3. Delivery (needs `node-identity`)
- [ ] 3.1 Claim answer `secrets: [{name, kind, version, sealed}]`, HPKE to the claiming node's encryption key with info company|run|node|name|version; secret missing at claim (deleted after dispatch) → `setup_failed` naming it. Verify: sealed value opens only with that node's key and that run's info; claim answer not stored.
- [ ] 3.2 Node opens sealed values; MCP header env (`FACTORY_MCP_SECRET_<NAME>`), `spec.secrets` env vars, file secrets in `<run tmp>/secrets/` 0600 removed at run end and at node start; redaction of every value. Remove `secrets_file` and `internal/node/secrets.go`. Verify: `TestExternalMCPResolve`, `TestJetStreamExternalMCPRun` reworked against Core-held secrets; file secret gone after the run.
- [ ] 3.3 Leak scan (design D7): planted value absent from every stored record and log. Verify: the scan test fails when a value is deliberately logged.
- [ ] 3.4 Feed events `secret.set`, `secret.deleted`, `secret.delivered` (names only).

## 4. Docs
- [ ] 4.1 `docs/agent-configuration.md` (`spec.secrets`, secrets in Core), `docs/development.md` (master key setup and gopass backup; no `secrets_file`), `docs/factory-mcp.md`, `docs/runtime-protocol-v2.md` (claim `secrets`).
