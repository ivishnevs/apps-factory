Planned (owner 2026-11-03: D1–D8 confirmed). Not started. No migration (not released). Order: catalog model,
projects and assignment, resolution and messaging, policies, seed and templates, UI. Can run in parallel with
`user-accounts`; `team-builder` builds on it.

## 1. Decide
- [x] 1.1 Owner confirms the company as owner of every shared resource and teams owning agents and projects (D1).
- [x] 1.2 Owner confirms projects bound to one team with assignment checks (D4).

## 2. Specs and docs
- [x] 2.1 Delta specs `company-teams`, `agent-hierarchy`, `review-policy`, `project-creation`; `openspec validate --strict`.
- [ ] 2.2 Docs: `agent-configuration.md` (Team kind, `spec.team`), `catalog.md` (teams documents, `company.yaml`),
  `projects.md` (project team, audit), `policies.md` (`teams` selector, reviewer per team), `messaging.md`.

## 3. Catalog model
- [ ] 3.1 `Team` kind under `teams/` (`ValidCatalogPath`, loader over `fs.FS`), `spec.team` required, lead and audit
  assignee validated, `reports_to` inside the team, leads are the owner's direct reports, team profile as default
  `extends`; `company.yaml` without `default_report`/`audit_assignee`.
- [ ] 3.2 `GET /catalog/teams` (teams with lead, agents, projects) replaces `GET /catalog/team`.

## 4. Projects and assignment
- [ ] 4.1 `Project.Team` (required, validated); `POST /projects` and `factory_project_create` (creating agent's
  team); change of team only without live tasks; catalog save refuses removing a used team.
- [ ] 4.2 Assignment, delegation and task creation refuse agents of another team (`409 team_mismatch`).

## 5. Resolution, messaging, policies
- [ ] 5.1 Owner's unaddressed messages → the project team's lead (chat names a team outside projects); onboarding
  audit → team's audit assignee; review reviewer role/agent resolved in the project's team.
- [ ] 5.2 Default `can_message` = own team + owner; explicit entries across teams.
- [ ] 5.3 Policy rules' `teams` selector; mandatory rules unchanged.

## 6. Seed and templates
- [ ] 6.1 Rewrite `configs/`, `configs/<company>/` and `configs/templates/*` with teams; tests and fixtures updated.

## 7. Web UI
- [ ] 7.1 Team pages (`/c/{company}/teams/{team}`), team filter on tasks, chats and feed, team in the project form,
  team picker in the owner chat.

## 8. Verification
- [ ] 8.1 `make check` green plus NATS tests with `-p 1`; tests: cross-team assignment and delegation refused,
  agent-created project's team, owner message routing per team, reviewer role per team, cross-team message refused
  without `can_message`, team-only policy rule, used team removal refused, two teams on one node.
