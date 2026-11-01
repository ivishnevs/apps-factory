Planned: not scheduled. Confirm task 1 before starting the rest. No data migration or backward compatibility:
replace the file-based catalog outright.

## 1. Decide
- [ ] 1.1 Owner confirms catalog in Core storage with versions and apply without restart (D1, D2).
- [ ] 1.2 Owner decides one Core per company or one Core for many companies.
- [ ] 1.3 Owner decides which catalog sections clients may edit (node hooks, MCP servers, quota keys, command guards).
- [ ] 1.4 Owner confirms the review rule type (D3) and its first scope (required/optional and default reviewer; paths later).

## 2. Specs and docs
- [ ] 2.1 Delta specs for agent configuration, policies and exits; drop `skip_specs`; `openspec validate --strict`.
- [ ] 2.2 Update `docs/agent-configuration.md`, `docs/policies.md`, `docs/development.md`, `docs/explicit-exits-contract.md`.

## 3. Catalog storage and API
- [ ] 3.1 Catalog tables (agents, profiles, policies, prompts, skills registry, company settings) with versions, author and time.
- [ ] 3.2 Load and validate from storage with the existing validator; seed from the repository's `configs/`.
- [ ] 3.3 API: list, get, create, update, delete, history and rollback; field-level validation errors; apply without restart.
- [ ] 3.4 Agent-proposed catalog changes go through owner approval (`factory.config.*`).

## 4. Company provisioning
- [ ] 4.1 Create a company and its owner through API and CLI.
- [ ] 4.2 Starter team templates (solo builder; PO + developer + reviewer; PO + designer + developer + QA).

## 5. Review policy
- [ ] 5.1 Policy rule `type: review` (required/optional per role or agent, default reviewer); validation.
- [ ] 5.2 Enforce at `factory_exit_done` (refuse without an approval bound to the PR when review is required); default reviewer at `submit`.
- [ ] 5.3 Path rules (e.g. requirement changes need the product owner) once specs live in git (`openspec-in-git`).

## 6. UI
- [ ] 6.1 Web: company setup from a template, team editor (agents, roles, reporting lines), prompt and policy editors with diff and history.
- [ ] 6.2 Terminal UI: read-only view of the catalog (optional).

## 7. Verification
- [ ] 7.1 `make check` green; tests for storage-backed load, validation errors, apply without restart with running runs keeping their snapshot, review enforcement at done.
