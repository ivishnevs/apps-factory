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
