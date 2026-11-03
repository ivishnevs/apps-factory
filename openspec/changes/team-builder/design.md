## Context

See proposal. Related: `company-teams` (teams, `spec.team`, `project.team`), `company-self-service` (stored catalog
D1, templates 4.2, Team and Companies pages 6.1), `user-accounts` (company created at sign-up, roles),
`skill-registry` (built-in source, installed skills, picker), `core-secret-store` (secrets, wait before dispatch),
`node-identity` (node enrollment by the company's owner or admin).

## Decisions (owner 2026-11-03)

### D1. Presets are product content, versioned with the release
```
presets/
  agents/<name>/preset.yaml   # metadata (title, description, role, tags), the Agent document, Factory skills,
                              # secrets it needs (names and what for)
  agents/<name>/prompt.md
  teams/<name>.yaml           # team document, agent presets with reports_to and lead, optional profile/policies
```
`configs/templates/*` and `prompts/roles/*` become presets. Presets name skills of the built-in source only. A
preset's version is the release's preset digest; it is shown in the UI and recorded on copies.

### D2. Copy, never link
Creating from a preset writes ordinary catalog documents in one new catalog version: the team document (team
preset), each agent with `spec.team` and `metadata.preset: <name>@<version>`, its prompt as a company document
(`prompts/<agent>.md`), installed skills for the preset's Factory skills (`skill-registry` D4), and the preset's
profile and policies under team-prefixed names. After the copy the company depends on nothing on disk: the
shared-prompt fallback of `company-self-service` D1 is not used. A later preset release only produces an "update
available" hint with a diff; nothing changes without the owner saving it. Agent names from a preset default to
`<team>-<preset>` (unique per company, `company-teams` D3) and can be changed.

### D3. Flows
1. First team: the company exists (`user-accounts` D1); `POST /catalog/teams {from_preset|team}`.
2. More teams: the same call; each team gets its own agents and projects.
3. Agents: `POST /catalog/agents {team, from_preset, name, overrides}` or a custom `{team, agent}`;
   `GET/PUT/DELETE /catalog/agents/{name}` (delete refused while others report to it or it leads its team).
Every write builds documents and goes through the existing catalog save (`base` version, loader validation, 409 on
a newer version), so the YAML editor and the forms see the same catalog.

### D4. Forms and setup status
Agent form: name, title/description, team, role, `reports_to`, `can_message`, runtime (provider, model, quota
key), prompt (Markdown; prefilled from a preset), skills (installed skills available to the team), policies.
Loader errors map back to fields. Advanced: open the document in the YAML editor.
`GET /setup` lists what blocks work: secrets agents need that are not set (from `core-secret-store` D3), no
enrolled node, a team without projects. The wizard and a banner show it; nothing is refused because of it, and
runs wait with a clear reason (`core-secret-store` waits before dispatch; no node means queued).

### D5. Preset gallery
Agent presets (iOS developer, product owner, designer, QA, reviewer, builder, game designer, ...) and team presets
(solo, product team, full team, game-dev, SaaS) with role, skills, secrets needed and version.

## Risks / Trade-offs

- Copies drift from presets by design; update hints keep clients aware without surprises.
- A preset referencing a Factory skill removed from a release: the copy keeps its installed hash (blob stays),
  new copies drop it with a validation message.
