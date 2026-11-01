In progress (owner 2026-11-01: D1–D6 confirmed, start). No data migration or backward compatibility: the file
catalog becomes seed data. Order: loader over fs.FS (no behaviour change), storage and per-company catalogs in
Core, API and CLI, provisioning, review policy, UI.

## 1. Decide
- [x] 1.1 Owner confirms catalog in Core storage with versions and apply without restart (D1, D2).
- [x] 1.2 One Core serves many companies (D4).
- [x] 1.3 Clients edit their whole catalog, node hooks, MCP servers, quota keys and command guards included (D5).
- [x] 1.4 Review rule type (D3), first scope required/optional and default reviewer; paths later.

## 2. Specs and docs
- [x] 2.1 Delta specs (`company-catalog`, `review-policy`); drop `skip_specs`; `openspec validate --strict`.
- [ ] 2.2 Update `docs/agent-configuration.md`, `docs/policies.md`, `docs/development.md`, `docs/explicit-exits-contract.md`.

## 3. Catalog storage and API
- [x] 3.1 Loader over `fs.FS` (`LoadCompanyCatalogFS`; disk through `os.DirFS`), skills registry included; no behaviour change.
- [x] 3.2 Migration: `catalog_versions`, `catalog_documents`; store save (validate with the loader, new current version in one transaction), history, read of a version.
- [x] 3.3 Seed a company without a stored catalog from its repository directory and the prompts it names (author `seed`).
- [x] 3.4 Core resolves the catalog per company (D4): `c.catalog(company)` replaces `Core.Agents`/`Skills`/`NodeHooks`/`DefaultReport`/`AuditAssignee`; loops run per company; swap after a save.
- [x] 3.5 API: `GET /catalog[?version]`, `GET /catalog/document`, `GET /catalog/history`, `POST /catalog` (put/delete on top of a base, `dry_run` validates), `POST /catalog/rollback`; loader errors name the document.
- [x] 3.6 CLI: `factory catalog [VERSION]|history|get|put|rm|validate|rollback|pull DIR|push DIR`.
- [x] 3.7 Agent-proposed catalog changes through owner approval: `factory_catalog_get` (read), `factory_catalog_change` (always needs the owner's approval of the exact call; author = the agent).

## 4. Company provisioning
- [x] 4.1 Create a company and its owner token through API and CLI: `POST /companies` (operator token), company owner tokens `fot_…` (`/owner-tokens`), `factory companies|templates|owner-tokens`.
- [x] 4.2 Starter team templates `configs/templates/{solo,product-team,full-team}` with role prompts in `prompts/roles/`.

## 5. Review policy
- [ ] 5.1 Policy rule `type: review` (required/optional per role or agent, default reviewer); validation.
- [ ] 5.2 Enforce at `factory_exit_done` (`409 review_required` without an approval bound to the PR); default reviewer at `submit`.
- [ ] 5.3 Path rules (e.g. requirement changes need the product owner): changed files in the exit facts.

## 6. UI
- [ ] 6.1 Web: company setup from a template, team editor (agents, roles, reporting lines), prompt and policy editors with diff and history.
- [ ] 6.2 Terminal UI: read-only view of the catalog (optional).

## 7. Verification
- [ ] 7.1 `make check` green plus NATS tests with `-p 1`; tests for storage-backed load, validation errors, apply without restart with running runs keeping their snapshot, two companies side by side, review enforcement at done.
