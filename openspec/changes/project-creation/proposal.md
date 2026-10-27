## Why
A new project can be created only by the owner from the CLI (`factory projects create ID ABS_WORKSPACE`) or `POST /projects`, and only after creating a git repository on the machine by hand and passing its absolute path. The web and terminal UIs have a new-task form but no new-project form, and no agent can create a project: when the owner picks an idea from a research round (project `app-ideas`), the team cannot start it on its own. Projects without a remote (such as `app-ideas`) also get no pull requests. Adding a repository that already exists has a trap: when it tracks `openspec/` in git, the first run's materialization of Core's (empty) OpenSpec tree wipes it (this happened with `kickclimb`); the owner has to import the tree and untrack it by hand first.

## What Changes
- **New project form** in the web UI (`owner-web`) and the terminal UI (`owner-tui`): id, name, description, OpenSpec on/off, repository mode (new local, new private GitHub, existing project with path/URL, branch and "Run an audit first"); Core's refusal is shown under the form, which stays open.
- **The node provisions the repository** when no workspace is given: by default an empty local git repository with an initial commit on `main` under the node's projects root; optionally a private GitHub repository under the owner's account (created through the node's `gh` credentials), cloned locally with `repo_url` set so exits open pull requests. Core records the requested mode; the node that claims the project's first task run performs the setup and reports it back; no other run of the project starts meanwhile.
- **Existing projects are onboarded** (`repo_mode: existing`, a workspace path and/or a repository URL that the node clones): before any task runs, the node imports the repository's OpenSpec tree into Core and stops tracking it in git; optionally Core creates an onboarding audit task (project-audit skill) for the configured agent. An existing workspace path without a repository mode keeps today's behaviour.
- **Agent tool `factory_project_create`** for agents whose configuration allows it (the owner's direct reports, i.e. the CEO, by default): creates the project through an owner approval (`action-approvals`); nothing is created until the owner approves; the approval shows id, name, description and repository mode. After approval the agent may create the first tasks in the new project.
- Every creation is a feed event naming who asked and who approved; provisioning and its failure are feed events too.

## Out of scope
Deleting or archiving projects, moving a project between nodes, repositories on hosts other than GitHub, choosing the GitHub organization (the repository goes to the account the node's `gh` is logged in to).

## Dependencies
`owner-web`, `owner-tui` (forms), `action-approvals` (approval of the agent tool), `task-worktrees` (workspaces and remotes), `factory-mcp` (tool), `core-owned-project-data` (OpenSpec import).

## Capabilities
### New Capabilities
- `project-creation`: create projects from the UIs and by agents with owner approval, with the node provisioning or onboarding the repository.
