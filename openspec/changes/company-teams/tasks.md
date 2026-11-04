Done (owner 2026-11-03: D1–D8 confirmed). No migration (the project's team is in its JSON body). Order: catalog model,
projects and assignment, resolution and messaging, policies, seed and templates, UI. Can run in parallel with
`user-accounts`; `team-builder` builds on it.

## 1. Decide
- [x] 1.1 Owner confirms the company as owner of every shared resource and teams owning agents and projects (D1).
- [x] 1.2 Owner confirms projects bound to one team with assignment checks (D4).

## 2. Specs and docs
- [x] 2.1 Delta specs `company-teams`, `agent-hierarchy`, `review-policy`, `project-creation`; `openspec validate --strict`.
- [x] 2.2 Docs: `agent-configuration.md` (Team kind, `spec.team`), `catalog.md` (teams documents, `company.yaml`),
  `projects.md` (project team, audit), `policies.md` (`teams` selector, reviewer per team), `messaging.md`.

## 3. Catalog model
- [x] 3.1 `Team` kind under `teams/` (`ValidCatalogPath`, loader over `fs.FS`), `spec.team` required, lead and audit
  assignee validated, `reports_to` inside the team, leads are the owner's direct reports, team profile as default
  `extends`; `company.yaml` without `default_report`/`audit_assignee`. Done: `internal/config/teams.go`
  (`Catalog.Teams`, `TeamOf`, `Lead`, `TeamAgents`), `TestTeams`, `TestTeamsRefused`.
- [x] 3.2 `GET /catalog/teams` (teams with lead, agents, projects) replaces `GET /catalog/team`.

## 4. Projects and assignment
- [x] 4.1 `Project.Team` (required, validated); `POST /projects` and `factory_project_create` (creating agent's
  team); change of team only without live tasks; catalog save refuses removing a used team. Done: required with
  several teams, the only team otherwise; `factory projects create --team`; `PATCH /projects/{id}` `team`.
- [x] 4.2 Assignment, delegation and task creation refuse agents of another team (`409 team_mismatch`). Done:
  `Store.TeamOf` hook checked on task insert and on every assignee change (`saveTaskNote`); conversation tasks only
  for the project's team.

## 5. Resolution, messaging, policies
- [x] 5.1 Owner's unaddressed messages → the project team's lead (chat names a team outside projects); onboarding
  audit → team's audit assignee; review reviewer role/agent resolved in the project's team. Done: `POST /messages`
  `team`; `@name` in a team's project only that team's lead.
- [x] 5.2 Default `can_message` = own team + owner; explicit entries across teams.
- [x] 5.3 Policy rules' `teams` selector; mandatory rules unchanged.

## 6. Seed and templates
- [x] 6.1 Rewrite `configs/`, `configs/<company>/` and `configs/templates/*` with teams; tests and fixtures updated.
  Done: `configs/` teams `core` (manager, manager-autonomous under manager, demo, claude-demo) and `calorie`;
  `smoke`; templates one team `product`; `company.yaml` removed; scripts create projects in their agent's team.

## 7. Web UI
- [x] 7.1 Team pages (`/c/{company}/teams/{team}`), team filter on tasks, chats and feed, team in the project form,
  team picker in the owner chat. Done: Teams page (teams, documents, history; `/team` redirects), team page (lead,
  audits, profile, agents, projects); filters shown with several teams; the chat lists the team's lead per project.

## 8. Verification
- [x] 8.1 `make check` green plus NATS tests with `-p 1`; tests: cross-team assignment and delegation refused,
  agent-created project's team, owner message routing per team, reviewer role per team, cross-team message refused
  without `can_message`, team-only policy rule, used team removal refused, two teams on one node. Done:
  `TestProjectTeams`, `TestOwnerMessageTeams`, `TestTeamRemovalInUse`, `TestTwoTeamsOneNode` (internal/core),
  `TestTeams`, `TestTeamsRefused`, `TestTeamPolicySelector` (internal/config).
