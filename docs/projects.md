# Projects

A project is a repository the team works in, with its tasks, OpenSpec tree, rules and feed. Projects are created
by the owner (`POST /projects`, `factory projects create`, the New project forms of the web and terminal UIs) or
by an agent allowed to, with the owner's approval (`factory_project_create`). OpenSpec spec:
`openspec/specs/project-creation`.

## Repository modes

| `repo_mode` | What the project gets | Fields |
| --- | --- | --- |
| `local` (default without `workspace` and `repo_url`) | a new git repository `<projects_root>/<id>` on the node, initial commit on `main` (README, `.gitignore` with `/openspec/` and `/.factory/`); no remote, so no pull requests | none |
| `github` | the same, plus a new **private** GitHub repository `<gh user>/<id>` created with the node's `gh` (the account `gh` is logged in to), `main` pushed, `repo_url` set: exits open pull requests | none |
| `existing` | a repository that exists: `workspace` (an absolute path on the node, used as it is) and/or `repo_url` (cloned to `<projects_root>/<id>` when no workspace is given); optional `default_branch`; `audit: true` runs an onboarding audit first | `workspace`, `repo_url`, `default_branch`, `audit` |
| none, with a `workspace` | today's behaviour: the path is used as it is, nothing is set up or onboarded | `workspace`, `repo_url`, `default_branch` |

The repository is set up **on the node, when the project's first task runs**: Core runs in Docker and never
touches the node's disk. Until then the project shows `setup.state: "pending"` and no workspace. The first task's
run sets the repository up before its runtime starts and reports it to Core; meanwhile no other run of the project
starts (other tasks show `project_setup` or `capacity` in `factory tasks why`). The feed records
`project.created` (who asked, who approved, the repository mode), then `project.provisioned` (node, path, URL)
or `project.setup_failed` with the reason. A failed setup ends that run `setup_failed`; its task
retries with the usual backoff, so fix the cause (for example `gh auth login` on the node) and the next attempt
continues where the last stopped (every step is idempotent).

`projects_root` is a `node.yaml` key (default `<state_dir>/projects`; on the owner's Mac `~/projects`). A local or
github setup never adopts a directory it did not create: `<projects_root>/<id>` must not exist or be empty (use
`existing` with that path instead).

## Onboarding an existing repository

`repo_mode: existing` sets the repository up on the node before any task runs: the workspace path is checked
(the top directory of a git repository) or `repo_url` is cloned under `projects_root`. The repository is left as
it is: nothing is committed or pushed, and its `openspec/` stays tracked, since OpenSpec lives in the
repository and is reviewed with the code ([OpenSpec](openspec.md)). New `local` and `github` repositories ignore
only `/.factory/`.

`audit: true` creates a ready task for the configured agent (`configs/company.yaml` `projects.audit_assignee`,
default `product-manager`) to audit the project with the `project-audit` skill: a read-only audit, OpenSpec
baseline specs of the current behaviour committed to the repository with its pull request and a report with
options. It is the project's first task, so its run
onboards the repository.

## Owner

```sh
factory projects create advent-gift "Advent gift app"                     # local repository
factory projects create advent-gift --repo-mode github --name "Advent gift"
factory projects create kickclimb /Users/me/projects/kickclimb --repo-mode existing --audit
factory projects create remote-app --repo https://github.com/me/remote-app.git --branch main   # cloned, onboarded
factory projects create app-ideas /Users/me/projects/app-ideas            # as before: no onboarding
```

`POST /projects`:

```json
{"id": "advent-gift", "name": "Advent gift", "description": "...", "repo_mode": "local",
 "workspace": "", "repo_url": "", "default_branch": "", "audit": false, "request_id": "..."}
```

Refusals are 400 (invalid id, empty name, unknown `repo_mode`, `local`/`github` with a workspace or URL,
`existing` without either, a relative workspace, `audit` outside `existing`, the audit agent not configured) or 409
(the id exists, the workspace belongs to another project, or a `request_id` reused with other input). The answer
is the project, with `repo_mode`, `created_by`, `approval_id` (agent requests) and `setup`
(`state` pending|running|ready|failed, `task_id`, `run_id`, `node_id`, `reason`, `audit`, `audit_task_id`,
`updated_at`).

## Agents

`factory_project_create` takes `project_id`, `name`, `description`, `repo_mode`, `workspace`, `repo_url`,
`default_branch`, `openspec` (bool), `audit` and `request_id`. It is offered to agents whose
`communication.can_create_projects` is true, by default the owner's direct reports (the CEO); see
[agent configuration](agent-configuration.md#hierarchy). Every call needs the owner's approval of that exact call
([approvals](approvals.md)): the agent requests it with `factory_owner_request_approval` and finishes the turn;
nothing exists before approval, a rejection leaves nothing, and the approved call creates exactly one project for
its `request_id` (a retry answers the same project). The owner's approval shows the call's arguments (id, name,
description, repository mode). Afterwards the creating agent may create tasks in the new project
(`factory_task_create` with `project_id`); tasks of other projects stay out of reach.
