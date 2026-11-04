# company-teams Specification

## Purpose
Let one company run several teams, each with its own agents, lead and projects, on the company's shared nodes,
secrets, skills and catalog.

## Requirements

### Requirement: Teams in the company catalog
A company's catalog SHALL contain one or more teams, each with a lead and an audit assignee among its agents, and every agent SHALL belong to exactly one team. Nodes, quota keys, secrets, skills, people, profiles, policies and catalog versions SHALL stay company resources shared by all teams.

#### Scenario: Agent without a team
- **WHEN** a catalog version holds an agent without `spec.team`
- **THEN** the save SHALL be refused naming the agent

#### Scenario: Two teams on one node
- **WHEN** a company has teams `gamedev` and `saas` and one node
- **THEN** the node SHALL run tasks of both teams and both SHALL use the company's secrets, skills and quota keys

### Requirement: Projects belong to a team
A project SHALL belong to exactly one team of its company. Tasks of a project SHALL be created for, assigned or delegated only to agents of that team. A project created by an agent SHALL belong to that agent's team.

#### Scenario: Assignment across teams
- **WHEN** the owner assigns a task of a `gamedev` project to an agent of team `saas`
- **THEN** Core SHALL refuse it with `team_mismatch`

#### Scenario: Project created by an agent
- **WHEN** the lead of team `saas` creates a project through an approved `factory_project_create`
- **THEN** the project SHALL belong to team `saas`

#### Scenario: Team removed while used
- **WHEN** a catalog save removes team `gamedev` while a project belongs to it
- **THEN** the save SHALL be refused naming the project

### Requirement: Team-scoped resolution
The owner's unaddressed messages, onboarding audits and review rules' reviewer roles SHALL resolve inside the project's team: its lead, its audit assignee, and the agent with the reviewer role in that team.

#### Scenario: Owner message in a project
- **WHEN** the owner writes in a `gamedev` project without addressing an agent
- **THEN** the message SHALL go to the `gamedev` team's lead

#### Scenario: Reviewer role per team
- **WHEN** a review rule names reviewer role `qa` and both teams have a `qa` agent
- **THEN** a developer of team `saas` SHALL be sent to review by the `saas` team's `qa` agent

### Requirement: Communication inside the team by default
An agent SHALL be able to message the agents of its team and the owner; messages to agents of other teams SHALL require explicit `can_message` entries.

#### Scenario: Message to another team
- **WHEN** a `gamedev` agent without explicit entries messages an agent of team `saas`
- **THEN** Core SHALL refuse the message

### Requirement: Policies select teams
Policy rules SHALL accept a `teams` selector next to roles and agents; mandatory rules SHALL apply to every team.

#### Scenario: Team-only rule
- **WHEN** a rule with `teams: [saas]` denies a tool
- **THEN** agents of team `saas` SHALL be denied it and agents of other teams SHALL NOT
