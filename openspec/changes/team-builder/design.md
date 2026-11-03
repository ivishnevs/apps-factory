## Context

See proposal. Related: `company-self-service` (stored catalog D1, team templates 4.2, Team and Companies pages
6.1), `user-accounts` (users create their companies, company per request), `skill-registry` (built-in source,
installed skills, picker).

## Decisions (owner 2026-11-03)

### D1. Presets are product content, versioned with the release
```
presets/
  agents/<name>/preset.yaml   # metadata (title, description, role, tags), the Agent document, Factory skills
  agents/<name>/prompt.md
  teams/<name>.yaml           # agent presets with reports_to, profile, policies, company.yaml, quota keys
```
`configs/templates/*` and `prompts/roles/*` become presets. Presets name skills of the built-in source only.
A preset's version is the release's preset digest; it is shown in the UI and recorded on copies.

### D2. Copy, never link
Creating from a preset writes ordinary catalog documents in one new catalog version: the agent document with
`metadata.preset: <name>@<version>`, its prompt as a company document (`prompts/<agent>.md`), and installed
skills for the preset's Factory skills (`skill-registry` D4). Team presets also write the profile, policies,
`company.yaml` and quota keys. After the copy the company depends on nothing on disk: the shared-prompt fallback
of `company-self-service` D1 is not used for companies created this way. A later preset release only produces
an "update available" hint with a diff; nothing changes without the owner saving it.

### D3. Flows
1. New company from a team preset: `POST /companies {name, team_preset}` → version 1 holds the whole team.
2. New empty company, then agents: `POST /catalog/agents {from_preset, name, overrides}` or a fully custom
   `POST /catalog/agents {agent}`.
3. Edit an agent: `GET/PUT /catalog/agents/{name}`; delete with `DELETE` (refused while others report to it).
Every write builds documents and goes through the existing catalog save (`base` version, loader validation,
409 on a newer version), so the YAML editor and the forms see the same catalog.

### D4. Agent form
Fields: name, title/description, role, `reports_to`, `can_message`, runtime (provider, model, quota key),
prompt (Markdown editor; from a preset prefilled), skills (installed Factory and company skills, discovery
categories), policies (from the catalog). Loader errors map back to fields. Advanced: open the document in the
YAML editor.

### D5. One team per company
A company's catalog is its team. Several teams inside a company (groups under different leads) are out of
scope; `reports_to` already expresses hierarchy.

## Risks / Trade-offs

- Copies drift from presets by design; update hints keep clients aware without surprises.
- A preset referencing a Factory skill removed from a release: the copy keeps its installed hash (blob stays),
  new copies drop it with a validation message.
