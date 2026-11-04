## ADDED Requirements

### Requirement: Owner web API
Factory SHALL serve the owner's web UI API over the owner client, holding the owner token itself and never returning it to the browser.

#### Scenario: Action without the web header
- **WHEN** a request posts an owner action without the `X-Factory-Web: 1` header
- **THEN** the API SHALL refuse it with 400 and perform nothing

### Requirement: Confirmed owner actions
The web UI SHALL ask for confirmation before every owner action.

#### Scenario: Cancel a task from a phone
- **WHEN** the owner taps cancel on a task
- **THEN** the UI SHALL show what will happen and act only after the owner confirms

### Requirement: Task materials
The web UI SHALL show what every task produced: report attachments, artifact versions, submission artifacts and pull requests, on the task and in a company-wide Library, viewable and downloadable without leaving the UI.

#### Scenario: Read a research report
- **WHEN** a finished task's report attached a markdown file
- **THEN** the task page SHALL list the file under Materials with the report summary, who made it and when
- **AND** opening it SHALL render the markdown in a viewer with a download button, served sandboxed and typed as markdown

#### Scenario: Find a file in the Library
- **WHEN** the owner opens the Library and filters by project or type or searches by name
- **THEN** the UI SHALL list the matching materials across projects, newest first, each linking to its task
