## Context

See proposal. Related: `company-self-service` (stored catalog, review rules D3), `hierarchy` (reporting tree,
default report), `project-creation` (onboarding audit assignee), `messaging` (`can_message`), `user-accounts`
(company roles), `team-builder` (presets and wizard on top of teams), `skill-registry` (`scope: team:<name>`).

## Decisions (owner 2026-11-03)

### D1. Company owns resources, team owns agents and projects
Company: nodes, quota keys, secrets, skill sources and installed skills, people and roles, catalog versions,
profiles, policies, MCP servers, node hooks, `rules`, `time_zone`. Team: its agents, lead, audit assignee and
projects. Teams are a soft boundary enforced by Core checks; the hard boundary stays the company.

### D2. Team document
```yaml
apiVersion: factory/v1
kind: Team
metadata: {name: gamedev, description: Small games for iOS}
spec:
  lead: gamedev-lead          # an agent of this team reporting to owner; gets the owner's unaddressed messages
  audit_assignee: gamedev-lead # onboarding audits of this team's projects (default: lead)
  profile: gamedev            # optional: default profile for the team's agents (extends the company's)
```
Path `configs/<dir>/teams/<name>.yaml`; `teams` joins the catalog directories (`ValidCatalogPath`). A catalog
needs at least one team. Team names follow agent name rules.

### D3. Agents
`spec.team` is required. `reports_to` is an agent of the same team or `owner`; the agents reporting to `owner` are
exactly the team leads, one per team (its `spec.lead`). Agent names stay unique per company (tasks, runs, feed and tools address agents by name).
A team's `profile` is the default `extends` of its agents that name none.

### D4. Projects
`project.team` is required at creation and validated against the catalog. A task of a project may be created for,
assigned or delegated to an agent of that team only; Core refuses otherwise (`409 team_mismatch`). A project
created by an agent (`factory_project_create`) belongs to that agent's team. Changing a project's team is allowed
while it has no live tasks. A catalog save that removes a team still referenced by a project is refused.

### D5. Resolution inside the team
- Owner's unaddressed message in a project → the project team's lead; outside a project → the team the owner
  picked in the chat (the UI always sends one); with one team, its lead.
- Onboarding audit → the project team's `audit_assignee`.
- Review rule `reviewer: <role>` → the agent with that role in the project's team; `reviewer: <agent>` must be in
  that team. A rule without `teams` applies to every team.

### D6. Communication
Default `can_message` of an agent: its team's agents and `owner`. Explicit `can_message` entries may name agents of
other teams. Watchers and conversations are unchanged.

### D7. Policies
Rules gain an optional `teams: [...]` selector next to `roles`/`agents`; mandatory rules still apply to everyone.

### D8. No migration
`company.yaml` loses `owner.default_report` and `projects.audit_assignee` (refused as unknown fields). The seed
catalogs (`configs/`, `configs/<company>/`) and `configs/templates/*` are rewritten with teams; the stored catalog
is reseeded in development.

## Risks / Trade-offs

- A bug in assignment or messaging checks leaks work between teams of the same owner; tests cover assignment,
  delegation, project creation by agents, messaging and reviewer resolution.
- One catalog version for all teams: concurrent edits of two teams conflict (409, retry on the new version).
- One node and one quota key serve all teams: a busy team can starve another until per-team limits exist.
