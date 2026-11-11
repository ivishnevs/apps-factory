## ADDED Requirements

### Requirement: Tasks remembers the picked project
The web Tasks page SHALL remember, per company in the person's browser, the project last picked or opened through
`?project=`, and SHALL show it again when Tasks is opened without `?project=`. An explicit All projects SHALL be
remembered as well; a remembered project that no longer exists or is archived SHALL be forgotten, not restored.

#### Scenario: Back to Tasks from the sidebar
- **WHEN** the owner picks the tracely project on Tasks, opens a task and then clicks Tasks in the sidebar
- **THEN** Tasks SHALL open filtered to tracely

#### Scenario: All projects picked
- **WHEN** the owner picks All projects and later opens Tasks without `?project=`
- **THEN** Tasks SHALL show all projects

#### Scenario: Remembered project archived
- **WHEN** the remembered project has been archived
- **THEN** Tasks SHALL show all projects and forget the remembered one
