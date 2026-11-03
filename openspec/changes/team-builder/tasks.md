Planned (owner 2026-11-03: D1–D5 confirmed, everything copied into the company's catalog). Not started. Depends
on `skill-registry` 3.x (built-in source, installed skills) and `user-accounts` 5.2 (users create companies).

## 1. Decide
- [x] 1.1 Owner confirms presets copied into the catalog, never linked (D2).
- [x] 1.2 Owner confirms flows: team preset, or empty company with preset or custom agents (D3).

## 2. Specs and docs
- [x] 2.1 Delta spec `team-builder`; `openspec validate --strict`.
- [ ] 2.2 Docs: `catalog.md` (presets, copies), `agent-configuration.md` (`metadata.preset`), `owner-ui.md`.

## 3. Presets
- [ ] 3.1 `presets/agents` and `presets/teams` from `configs/templates/*` and `prompts/roles/*` (iOS developer,
  product owner, designer, QA, reviewer, builder, ...); loader and validation of presets in `make check`.
- [ ] 3.2 Copy into documents: agent, prompt as a company document, installed Factory skills, team-level
  documents; `metadata.preset`.

## 4. API
- [ ] 4.1 `GET /presets/agents`, `GET /presets/teams` (with versions and skills).
- [ ] 4.2 `POST /companies {team_preset}`; `POST /catalog/agents {from_preset|agent}`, `GET/PUT/DELETE
  /catalog/agents/{name}` over the catalog save; field-level errors; refusal to delete a lead.
- [ ] 4.3 Update hints: copies whose preset version is older, with a diff.

## 5. Web UI
- [ ] 5.1 Company wizard: team preset or empty company.
- [ ] 5.2 Add agent: preset gallery or custom; agent form (D4) with the skill picker from `skill-registry`.
- [ ] 5.3 Team page: agents as cards with edit/delete, preset update hints; YAML editor stays.

## 6. Verification
- [ ] 6.1 `make check` green plus NATS tests with `-p 1`; tests: company from each team preset loads and runs
  without repository prompts on disk, agent from a preset with overrides, conflict on a stale version, lead
  deletion refused.
