## ADDED Requirements

### Requirement: Owner creates a project from the UI
The web and terminal UIs SHALL offer a New project form (id, name, description, OpenSpec on/off, repository mode: new local, new private GitHub, or existing with path/URL, branch and an optional audit) that creates the project through `POST /projects` with a request id per submit and shows Core's refusal under the form, which stays open.

#### Scenario: Owner starts a project from the phone
- **WHEN** the owner submits the form with id `advent-gift`, a name and the default repository mode
- **THEN** Core SHALL create the project and it SHALL appear in the projects list and the task form

#### Scenario: Owner adds an existing project from the form
- **WHEN** the owner picks the repository mode Existing project, gives a folder on the node and/or a repository URL (optionally a branch) and leaves "Run an audit first" on
- **THEN** the form SHALL send `repo_mode` `existing` with `workspace`, `repo_url`, `default_branch` and `audit` true, and SHALL refuse to send without a folder or URL

### Requirement: Core provisions the repository
When no workspace or repository URL is given, the project SHALL get a git repository without manual setup: by default a local repository with an initial commit on `main` under the node's projects root, or, when the owner chooses it, a private GitHub repository created through the node's GitHub CLI, cloned locally with `repo_url` set. The node that claims the project's first task run SHALL set it up before the run starts and report it to Core; no other run of the project SHALL start until the setup finished, and a failed setup SHALL name its reason in the project's feed. An explicit workspace path without a repository mode SHALL keep today's behaviour.

#### Scenario: Project without a workspace
- **WHEN** a project is created without a workspace
- **THEN** its first task SHALL run in a fresh repository with a `main` branch, without any manual setup

#### Scenario: GitHub CLI not logged in
- **WHEN** the owner chose a private GitHub repository and the node's `gh` is not authenticated
- **THEN** the first run SHALL end `setup_failed`, the project's feed SHALL say that `gh` is not authenticated on that node, and the task's next attempt SHALL retry the setup

### Requirement: Existing projects are onboarded
A project created with repository mode `existing` (a workspace path on the node and/or a repository URL the node clones) SHALL be onboarded before any of its tasks runs: the repository's OpenSpec tree SHALL be imported into Core and, when git tracks it, a commit SHALL stop tracking it (pushed to the default branch, else to the first task's branch, else kept local). When the owner asks for an audit, Core SHALL create a ready audit task for the configured agent.

#### Scenario: Existing repository tracks openspec
- **WHEN** the owner adds an existing repository whose git tracks `openspec/`
- **THEN** the first task's run SHALL see the repository's OpenSpec tree (imported into Core, not wiped) and the repository SHALL no longer track it

#### Scenario: Audit first
- **WHEN** the owner adds an existing project with "Run an audit first"
- **THEN** Core SHALL create one ready audit task assigned to the configured agent (default `product-manager`), and its run SHALL onboard the repository first

### Requirement: Agents create projects only with owner approval
An agent allowed to create projects SHALL do so only through `factory_project_create`, which SHALL wait for the owner's approval; no project SHALL exist before approval, a rejection SHALL leave nothing behind, and an approved call SHALL create exactly one project for its request id. The creating agent MAY then create tasks in the new project. Every creation SHALL be a feed event naming who asked and who approved.

#### Scenario: CEO starts the chosen idea
- **WHEN** the CEO calls `factory_project_create` for the idea the owner picked
- **THEN** the owner SHALL get an approval showing the id, name, description and repository mode, and the project SHALL be created only after approval
