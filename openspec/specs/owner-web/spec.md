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

### Requirement: Project pause on the project
The web UI SHALL show a project's active pause where the project is shown: a Paused badge in the Projects list, and on
the project's page a banner with the reason, who paused it and when, with Resume for owners and admins; a running
project's page SHALL offer Pause to owners and admins.

#### Scenario: Paused project in the list
- **WHEN** the owner opens Projects while tracely has a project pause
- **THEN** tracely's card SHALL show Paused

#### Scenario: Resume from the project page
- **WHEN** an owner opens the paused tracely's page and confirms Resume
- **THEN** the pause SHALL be released and the banner SHALL give way to the Pause button

### Requirement: Skills page tells availability from use
The web Skills page SHALL describe an installed skill's scope as who may be given it ("Any agent may use", "Team NAME
only", "Agent NAME only") and SHALL list, for each installed skill, the agents whose `spec.skills.include` names it, or
say that no agent has it yet.

#### Scenario: Company-wide skill used by two agents
- **WHEN** `release-readiness` is installed company-wide and only the CEO, PM and App Store Publisher include it
- **THEN** its row SHALL read "Any agent may use" and "Used by appstore-publisher, ceo, product-manager", never "Every agent"

### Requirement: A task's result leads its materials
A finished task's page SHALL lead with the materials the agent named as the result in its last accepted submission or done exit, and without such materials with the newest material that has content. Materials outside the result SHALL be folded under "Earlier versions", grouped by artifact with their approvals, and every row SHALL name its artifact and version.

#### Scenario: Screenshots task with samples
- **WHEN** the owner opens a done screenshots task whose last submission named the final gallery, and that produced two approved samples and a storyboard
- **THEN** the final gallery SHALL be the lead card and the samples and the storyboard SHALL be under "Earlier versions" with the board's approval comments

### Requirement: Company rules in Settings
The web Settings SHALL show the company rules every agent's brief carries, let owners and admins edit and save them within the 8 KB limit, and show them read-only to members.

#### Scenario: Owner edits the rules
- **WHEN** an owner opens Settings → Company rules, changes the text and saves
- **THEN** the next brief of every agent SHALL carry the new text

### Requirement: Comment outcomes and closed-task actions on the task page
The task page SHALL show each owner comment's outcome (done, forwarded to whom, declined and why) or "not handled yet", and SHALL offer no comment box on a DONE or CANCELLED task, offering "Reopen with comment" and "Ask the lead about it" instead.

#### Scenario: Done task
- **WHEN** the owner opens a DONE task
- **THEN** the page SHALL show "Reopen with comment" and "Ask the lead about it" and no comment box

### Requirement: Archived projects stay out of everyday lists
Home's project list, the Chat page and the new-task form SHALL show only active projects; archived projects SHALL remain on the Projects page under "archived".

#### Scenario: Nine archived projects
- **WHEN** a company has three active and nine archived projects
- **THEN** Home SHALL list three projects and the Chat page SHALL show chats for those three only

### Requirement: Team questions in the lead's chat
The web chat with a project's lead SHALL show the open owner questions and approvals of the project's other agents as cards naming the asking agent and its task, answerable in place. The task page SHALL show its open question or approval with options and an answer box. An answer given anywhere SHALL close the item everywhere.

#### Scenario: App Release asks while the owner talks to the CEO
- **WHEN** App Release asks the owner which asc profile to use on a tracely task
- **THEN** the card SHALL appear in the owner's tracely chat with the CEO and on the task page, and answering it there SHALL answer App Release and clear the Inbox item

### Requirement: New-project errors are visible
Submitting the new-project form with invalid fields SHALL bring the first invalid field into view and focus it.

#### Scenario: Error above the fold
- **WHEN** the owner presses Create project at the bottom of the sheet and the ID field above is refused
- **THEN** the sheet SHALL scroll to the ID field and focus it, showing its error
