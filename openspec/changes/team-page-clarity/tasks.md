## 1. Design

- [x] 1.1 Wording and layout for the lead, onboarding agent, team settings and review roles; owner approves.
      Mockup approved by the owner 2026-11-12 (https://claude.ai/artifact/KRePSTaBkscYfF2fK8HnsP).

## 2. Implementation

- [x] 2.1 Team page: plain explanations, team settings in readable form, who does what. "How this team runs" (lead,
      onboarding a repository, team settings with the overriding agents), "Who does what", who approves, agents,
      projects; `GET /catalog/teams` carries `settings` per team and `assign_scope`, `can_message` per agent.
- [x] 2.2 Change the lead, the onboarding agent and team settings from the page; tests. `PUT /catalog/teams/{name}`
      (owners and admins; a catalog change the loader validates); `TestTeamPageEdits`.
- [x] 2.4 Agent card: may message (multi-select) and gives tasks to (none / direct reports / subtree / chosen
      agents) edited with controls, saved as a catalog change; team page columns "May message" and "Gives tasks to".
      A scope saved as none stays none (the agent document writes the scope word).
- [x] 2.3 Same wording in the team wizard, agent cards and docs ("team settings" for the profile, docs/catalog.md,
      docs/agent-configuration.md).
