## ADDED Requirements

### Requirement: Owner creates a project from the UI
The web and terminal UIs SHALL offer a New project form (id, name, description, OpenSpec on/off, repository mode) that creates the project through `POST /projects` with a request id per submit and shows Core's refusal under the form, which stays open.

#### Scenario: Owner starts a project from the phone
- **WHEN** the owner submits the form with id `advent-gift`, a name and the default repository mode
- **THEN** Core SHALL create the project and it SHALL appear in the projects list and the task form

### Requirement: Core provisions the repository
When no workspace or repository URL is given, Core SHALL provision a git repository for the project: by default a local repository with an initial commit on `main`, or, when the owner chooses it, a private GitHub repository cloned locally with `repo_url` set. An explicit workspace path or repository URL SHALL keep today's behaviour.

#### Scenario: Project without a workspace
- **WHEN** a project is created without a workspace
- **THEN** its first task SHALL run in a fresh repository with a `main` branch, without any manual setup

### Requirement: Agents create projects only with owner approval
An agent allowed to create projects SHALL do so only through `factory_project_create`, which SHALL wait for the owner's approval; no project SHALL exist before approval, and a rejection SHALL leave nothing behind.

#### Scenario: CEO starts the chosen idea
- **WHEN** the CEO calls `factory_project_create` for the idea the owner picked
- **THEN** the owner SHALL get an approval showing the id, name, description and repository mode, and the project SHALL be created only after approval
