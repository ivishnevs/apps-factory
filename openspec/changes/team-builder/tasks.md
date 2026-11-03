Planned (owner 2026-11-03: D1–D5 confirmed, everything copied into the company's catalog). Not started. Depends on
`company-teams` (teams), `skill-registry` 3.x (built-in source, installed skills) and `user-accounts` 5.2 (company
at sign-up).

## 1. Decide
- [x] 1.1 Owner confirms presets copied into the catalog, never linked (D2).
- [x] 1.2 Owner confirms flows: teams from presets or empty in the existing company, agents from presets or custom,
  setup status that never blocks (D3, D4).

## 2. Specs and docs
- [x] 2.1 Delta specs `team-builder`, `company-catalog`; `openspec validate --strict`.
- [ ] 2.2 Docs: `catalog.md` (presets, copies, setup status), `agent-configuration.md` (`metadata.preset`),
  `owner-ui.md` (wizard, forms).

## 3. Presets
- [ ] 3.1 `presets/agents` and `presets/teams` from `configs/templates/*` and `prompts/roles/*` plus game-dev and
  SaaS teams; loader and validation of presets in `make check`.
- [ ] 3.2 Copy into documents: team, agents with `spec.team` and `metadata.preset`, prompts as company documents,
  installed Factory skills, team-prefixed profile and policies.

## 4. API
- [ ] 4.1 `GET /presets/agents`, `GET /presets/teams` (versions, skills, needed secrets).
- [ ] 4.2 `POST /catalog/teams {from_preset|team}`; `POST /catalog/agents {team, from_preset|agent}`,
  `GET/PUT/DELETE /catalog/agents/{name}` over the catalog save; field-level errors; refusal to delete a lead.
- [ ] 4.3 `GET /setup`: missing secrets per agent, no node, teams without projects.
- [ ] 4.4 Update hints: copies whose preset version is older, with a diff.

## 5. Web UI
- [ ] 5.1 Wizard: team (preset or empty) → agents → missing secrets → node (if none) → first project.
- [ ] 5.2 "New team", add agent (preset gallery or custom), agent form (D4) with the skill picker.
- [ ] 5.3 Team pages: agents as cards with edit/delete, preset update hints, setup banner; YAML editor stays.

## 6. Verification
- [ ] 6.1 `make check` green plus NATS tests with `-p 1`; tests: team from each preset loads and runs without
  repository prompts on disk, second team leaves the first unchanged, agent from a preset with overrides, conflict on
  a stale version, lead deletion refused, setup status names a missing secret.
