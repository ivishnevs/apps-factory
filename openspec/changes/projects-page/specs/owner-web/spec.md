## ADDED Requirements

### Requirement: Projects in the menu
The web SHALL have a Projects item in the menu listing the company's projects with their state, and a page per
project with its overview, direction and rules, settings and links to its tasks, chat, library and costs.

#### Scenario: Owner looks for a project
- **WHEN** the owner opens Projects
- **THEN** each project SHALL show its team, repository, counts of open work by column and what needs the owner, and
  opening one SHALL show its page

#### Scenario: Editing a project
- **WHEN** the owner changes a project's repository branch
- **THEN** it SHALL be done on the project's page, not in company Settings
