Planned (owner 2026-11-03: D1–D5 confirmed, everything copied into the company's catalog). Not started. Depends on
`company-teams` (teams), `skill-registry` 3.x (built-in source, installed skills) and `user-accounts` 5.2 (company
at sign-up).

## 1. Decide
- [x] 1.1 Owner confirms presets copied into the catalog, never linked (D2).
- [x] 1.2 Owner confirms flows: teams from presets or empty in the existing company, agents from presets or custom,
  setup status that never blocks (D3, D4).

## 2. Specs and docs
- [x] 2.1 Delta specs `team-builder`, `company-catalog`; `openspec validate --strict`.
- [x] 2.2 Docs: `catalog.md` (presets, copies, setup status), `agent-configuration.md` (`metadata.preset`),
  `owner-ui.md` (wizard, forms). Done: `catalog.md` (presets, copies, API, setup status), `agent-configuration.md` (`metadata.preset`), `development.md`; `owner-ui.md` (wizard, forms) moves with 5.x to `saas-web`.

## 3. Presets
- [x] 3.1 `presets/agents` and `presets/teams` from `configs/templates/*` and `prompts/roles/*` plus game-dev and
  SaaS teams; loader and validation of presets in `make check`. Done: `presets/agents/*` (builder, product-owner, developer, reviewer, designer, qa, ios-developer, game-designer), `presets/teams/*` (solo, product-team, full-team, game-dev, saas), `presets/company/` (base documents); `internal/presets.Load` checks them in `make check` (`TestPresets`); `configs/templates` and `prompts/roles` removed.
- [x] 3.2 Copy into documents: team, agents with `spec.team` and `metadata.preset`, prompts as company documents,
  installed Factory skills, team-prefixed profile and policies. Done: `presets.CopyTeam`/`CopyAgent`; companies created from a template and at sign-up are copies too.

## 4. API
- [x] 4.1 `GET /presets/agents`, `GET /presets/teams` (versions, skills, needed secrets). Done: `GET /presets/agents`, `GET /presets/teams`.
- [x] 4.2 `POST /catalog/teams {from_preset|team}`; `POST /catalog/agents {team, from_preset|agent}`,
  `GET/PUT/DELETE /catalog/agents/{name}` over the catalog save; field-level errors; refusal to delete a lead. Done: `internal/core/presets.go` (catalog saves on a base, field errors `{field, error}`, lead deletion refused).
- [x] 4.3 `GET /setup`: missing secrets per agent, no node, teams without projects. Done: `GET /setup`.
- [x] 4.4 Update hints: copies whose preset version is older, with a diff. Done: `update` in `GET /catalog/agents/{name}` (from/to version and the prompt's changes).

## 5. Web UI
Moved to `saas-web` (owner 2026-11-04: the web UI is reworked last, on the web that talks to Core directly, so the
wizard and forms are built once); the API above is their contract.
- [ ] 5.1 Wizard: team (preset or empty) → agents → missing secrets → node (if none) → first project.
- [ ] 5.2 "New team", add agent (preset gallery or custom), agent form (D4) with the skill picker.
- [ ] 5.3 Team pages: agents as cards with edit/delete, preset update hints, setup banner; YAML editor stays.

## 6. Verification
- [x] 6.1 `make check` green plus NATS tests with `-p 1` while NATS exists (`http-node-transport` removes them); tests: team from each preset loads and runs without
  repository prompts on disk, second team leaves the first unchanged, agent from a preset with overrides, conflict on
  a stale version, lead deletion refused, setup status names a missing secret. Done: `make check` green (NATS is gone); `TestPresets` (each team preset without repository prompts on disk, second team leaves the first unchanged, agent with overrides, taken names), `TestTeamBuilderAPI` (stale version 409, lead deletion refused, field errors, setup status names a missing secret, update hint).