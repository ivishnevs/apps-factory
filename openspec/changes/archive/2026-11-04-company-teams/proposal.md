Status: planned (owner request 2026-11-03: inside one company the owner runs several teams with different agents
and settings, each on its own projects, e.g. a game-dev team and a stronger SaaS team; every resource stays shared
by the company; decisions D1–D8 confirmed the same day, see design.md). Not started.

## Why

The company is the tenant and should stay the owner of everything shared: nodes, subscriptions and quota keys,
secrets, skills, people, catalog versions, policies and profiles. But the catalog of a company is one flat team
today:

- A project has no link to agents (`tasks.Project`): a task of any project may be assigned to any agent of the
  company.
- `company.yaml` has one `owner.default_report` (where the owner's unaddressed messages go) and one
  `projects.audit_assignee` for every project.
- Review rules resolve a reviewer role across the whole company; `can_message` and `reports_to` know no teams.
- `team-builder` assumed one team per company; starting a second team would mean a second company, with a second
  node, secrets and skills entered again, and one subscription's quota split across companies Core cannot see
  together.

## What Changes

- **Team as a catalog entity:** `configs/<dir>/teams/<team>.yaml` (name, description, lead, audit assignee,
  optional profile). Every agent belongs to exactly one team (`spec.team`).
- **Projects belong to a team** (`project.team`): tasks of a project go only to agents of its team; a project an
  agent creates belongs to that agent's team.
- **Team-scoped resolution:** review rules' reviewer roles, the onboarding audit assignee and the owner's default
  report (the team lead) resolve inside the project's team.
- **Communication inside the team by default:** an agent messages its team and the owner; other teams only through
  explicit `can_message`.
- **Policies may select teams** (`teams:` next to `roles`/`agents`); a team may extend the company profile.
- Company-level resources are unchanged and shared by all teams: nodes, quota keys, secrets, skills, people,
  catalog versions.
- `company.yaml` keeps company settings only (`rules`, `time_zone`); `owner.default_report` and
  `projects.audit_assignee` move to teams. No migration (not released): the seed catalog and templates are
  rewritten with teams.

## Out of scope

Per-team permissions for people (company roles only), per-team catalog versions and rollback, pinning a team to a
node, per-team quota limits, moving an agent between teams with live tasks.

## Impact

`internal/config` (teams, `spec.team`, validation, `loadCompany`), `internal/core` (assignment, owner messages,
review, onboarding audit, `/catalog/teams`, project creation), `internal/tasks` (`Project.Team`),
`internal/storage` (project team), `internal/factorytools` (project creation, messaging), `internal/policies`
(`teams` selector), `configs/` seed and `configs/templates/*`, `internal/web` + `web/` (team pages, team filter,
team in the project form), docs `agent-configuration.md`, `catalog.md`, `projects.md`, `policies.md`,
`messaging.md`.
