## Why
A new project can be created only by the owner from the CLI (`factory projects create ID ABS_WORKSPACE`) or `POST /projects`, and only after creating a git repository on the machine by hand and passing its absolute path. The web and terminal UIs have a new-task form but no new-project form, and no agent can create a project: when the owner picks an idea from a research round (project `app-ideas`), the team cannot start it on its own. Projects without a remote (such as `app-ideas`) also get no pull requests. Planned; not started.

## What Changes
- **New project form** in the web UI (`owner-web`) and the terminal UI (`owner-tui`): id, name, description, OpenSpec on/off, repository mode; Core's refusal is shown under the form, which stays open.
- **Core provisions the repository** when no workspace is given: by default an empty local git repository with an initial commit on `main` under the node's projects root; optionally a private GitHub repository under the owner's account (created through the node's `gh` credentials), cloned locally with `repo_url` set so exits open pull requests. An existing workspace path or repo URL still works as today.
- **Agent tool `factory_project_create`** for agents whose role allows it (CEO by default): creates the project through an owner approval (`action-approvals`); nothing is created until the owner approves; the approval shows id, name, description and repository mode. After approval the agent may create the first tasks in the new project.
- Every creation is a feed event naming who asked and who approved.

## Out of scope
Deleting or archiving projects, moving a project between nodes, importing an existing remote repository from the UI beyond pasting its URL, repositories on hosts other than GitHub.

## Dependencies
`owner-web`, `owner-tui` (forms), `action-approvals` (approval of the agent tool), `task-worktrees` (workspaces and remotes), `factory-mcp` (tool).

## Capabilities
### New Capabilities
- `project-creation`: create projects from the UIs and by agents with owner approval, with Core provisioning the repository.
