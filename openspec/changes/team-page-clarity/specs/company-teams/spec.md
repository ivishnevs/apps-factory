## ADDED Requirements

### Requirement: Team page explains the team's structure
The team page SHALL explain in plain words what the lead, the onboarding agent and the team settings do, whether
each is required and how to change it, and SHALL NOT show internal identifiers (such as a profile name) as the
only information.

#### Scenario: Owner opens a team
- **WHEN** an owner opens a team page
- **THEN** the page SHALL say that the lead gets the owner's messages and plans the work, that onboarding of an
  existing repository is done by the lead unless another agent is chosen, and SHALL show the team settings
  (subscription, model, limits, approvals, review rules) in readable form

### Requirement: Owners change the team's structure from the team page
Owners and admins SHALL be able to change a team's lead, its onboarding agent and its team settings from the team
page; Core SHALL validate each change as a catalog change and refuse an agent of another team, an invalid run time
limit or a subscription of another runtime. Each agent's messaging and assignment scope SHALL be shown in "who does
what" and edited on the agent's form with controls.

#### Scenario: Owner changes the lead
- **WHEN** an owner makes another agent of the team its lead
- **THEN** that agent SHALL report to the owner, and the old lead and its direct reports SHALL report to it

#### Scenario: Owner limits whom an agent gives tasks to
- **WHEN** an owner sets an agent's "gives tasks to" to no one and saves
- **THEN** the agent's document SHALL keep that scope and Core SHALL refuse the agent's tasks for others
