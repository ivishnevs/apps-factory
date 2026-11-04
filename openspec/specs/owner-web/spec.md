# owner-web Specification

## Purpose
Let the owner watch and steer the company from a browser, on a phone as well as a desktop, with the same reads and
actions as the terminal UI.

## Requirements

### Requirement: Owner web API
Core SHALL serve the web UI's API itself, in its own process, with each person's session, role and company; changes SHALL need the `X-Factory-Web: 1` header. Core SHALL act with its owner token for the web only when no sign-in is configured and `FACTORY_WEB_LOCAL=1` is set.

#### Scenario: Action without the web header
- **WHEN** a request posts an owner action without the `X-Factory-Web: 1` header
- **THEN** the API SHALL refuse it with 400 and perform nothing

#### Scenario: Member's session
- **WHEN** a signed-in member reads the board through the web API
- **THEN** Core SHALL answer with that member's company and role, as for the member's direct requests

#### Scenario: No sign-in, no local flag
- **WHEN** Core has no sign-in configured and `FACTORY_WEB_LOCAL` is not set
- **THEN** the web API SHALL answer 401 and never act with the owner token

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
