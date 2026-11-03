# Design

## D1 API contract and repository modes
`POST /projects` (`tasks.ProjectCreate`) keeps its fields; `workspace` becomes optional and two fields are new:
`repo_mode` (`local` | `github` | `existing`) and `audit` (bool).

- No `repo_mode`: without `workspace` and `repo_url` it is `local`; a `repo_url` alone (or `audit: true`) is
  `existing`; a `workspace` keeps today's behaviour exactly (the path is used as it is, no setup, no onboarding,
  `repo_mode` stays empty). Existing API callers, scripts and the CLI with a path are unchanged.
- `local` and `github` create the repository, so they refuse `workspace`, `repo_url`, `default_branch` and
  `audit`; `existing` needs `workspace` (absolute) and/or `repo_url`; `default_branch` keeps the old rule (needs
  `repo_url`).
- Refusals are 400 with the reason (id pattern, empty name, unknown mode, relative workspace, mode/field
  mismatch, audit outside existing, audit agent not configured) or 409 (id exists, workspace belongs to another
  project, request id reused with other input). The id rule is the old one (`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`),
  safe as a directory and a GitHub repository name.
- The answer is the project with the new fields `repo_mode`, `created_by` (`owner` or the agent),
  `approval_id` (agent requests) and `setup` (`state` pending|running|ready|failed, `task_id`, `run_id`,
  `node_id`, `reason`, `openspec_imported`, `openspec_untracked`, `audit`, `audit_task_id`, `updated_at`); `setup`
  is absent for projects that need none. `workspace` is `""` until the setup is ready.
- Schema: projects' `UNIQUE (company_id, workspace)` becomes a partial unique index `WHERE workspace <> ''`
  (several projects may wait for their repository). No data migration.
- CLI: `factory projects create ID [ABS_WORKSPACE] [DESCRIPTION] [--repo-mode M] [--repo URL] [--branch B]
  [--name NAME] [--audit]`; the second positional argument is the workspace when absolute, else the description.
  The CLI does not imply a mode either: a path without `--repo-mode existing` is today's behaviour.

## D2 Setup on the first claim, serialized by the writer slot
Core runs in Docker and cannot touch the node's disk, so the node sets the repository up. Core records the mode
and `setup.state: pending`. `storage.Dispatch` of a project's task while its setup is not ready gives the run
`RunSpec.project_setup` `{mode, name, description, workspace, repo_url, default_branch, openspec_dir}`, an empty
workspace, and records `setup.state: running` with the task and run. The run's writer slot is
`project-setup/<project>`: the `workspace_writer` unique index admits one such live run per project across all
nodes (two concurrent first claims cannot both set it up), and the task snapshot counts that slot as the project's
writer, so other tasks of the project wait (`capacity`). After a failure, other tasks keep waiting with reason
`project_setup` while the task that tried is still open (it retries with its backoff; `setup_failed` counts an
attempt as before); if that task is done or cancelled, the next task's run takes the setup over. Conversation runs
are never setup runs (they talk, they do not need the repository). A setup-pending task skips Core's OpenSpec
change check at dispatch (the tree may not be imported yet); the node's preflight checks it after the setup.

The node runs the setup before anything else of the run, then `POST /runs/{id}/project-setup` with the run's lease
(node tokens may call it for their own runs): `{lease, node_id, workspace, repo_url, default_branch, openspec:
[{path, content}], openspec_untracked}` or `{lease, node_id, error}`. In one transaction Core imports the
documents (those whose digest differs from Core's current version; nothing deleted), sets the project's workspace,
`repo_url`, `default_branch`, `setup.state: ready` (version + 1), rewrites the run's stored spec with the workspace
(and `workspace_mode` when there is a repository URL), and answers `{workspace, repo_url, default_branch,
workspace_mode, materialize}`: the manifest is computed after the import, so the first run materializes the
imported tree, not the empty one. A repeated report of a ready setup by the same run answers the same; a report of
a run that no longer owns the setup is 409. An error is stored as `setup.reason` with a `project.setup_failed`
feed event, and the run ends `setup_failed` before its runtime starts. The setup run keeps the setup slot until it
ends (its own work then happens in the new workspace): the first task's run is the only run of the project until
then, which over-serializes worktree-mode projects only for that first run.

## D3 Node side, idempotent
`node.yaml` `projects_root` (default `<state_dir>/projects`; `~/` expands; the owner's Mac uses `~/projects`).

- local: `<projects_root>/<id>`; `git init -b main`, README (name, description) and `.gitignore` (`/.factory/`,
  `/<openspec dir>/`), initial commit (fallback identity `App Factory <factory@localhost>` when git has none). A
  marker `.git/factory-project` (`<company>/<id>`) lets a retry reuse the repository; a non-empty directory without
  the marker is refused ("use repo_mode existing with this path"): the owner's files are never adopted.
- github: `gh` must be on PATH (else "not installed on node N") and `gh auth status` must pass (else "gh is not
  authenticated on node N: run gh auth login"); the repository is `<gh api user login>/<id>`, created with
  `gh repo create --private` unless `gh repo view` finds it (a retry), origin set to the URL `gh` reports, `main`
  pushed (a refused push names `gh auth setup-git`). `repo_url` = that URL, `default_branch` = main. Worker field
  `GH` overrides the binary (tests use a fake gh).
- existing: a `workspace` must be the top directory of a git work tree on the node; a `repo_url` alone is cloned
  to `<projects_root>/<id>` (a retry reuses a clone of the same URL; anything else there is refused).

## D4 OpenSpec onboarding of an existing repository
Every valid OpenSpec document under the project's OpenSpec directory is sent with the report (as `factory openspec
import`, invalid content refuses the setup). When git tracks the directory: refuse if the index has staged changes
(they would go into the commit), `git rm -r --cached`, add `/<dir>/` to `.gitignore`, commit "Keep OpenSpec in App
Factory: stop tracking <dir>/". Then, when there is an `origin` and the commit's parent is exactly the remote
default branch (`default_branch`, else `origin/HEAD`): push to the default branch (`pushed`); a refused push
(protected branch) pushes it as the first task's branch `af/<task>` instead (`task_branch`; the worktree of that
task starts from the remote task branch, so the untracking reaches the default branch with the task's pull
request); otherwise (no remote, the checkout holds other unpushed work, both pushes refused) it stays a local commit
(`local`). With `local` the remote copy still tracks the tree, but Core's tree now equals it, so materialization
no longer wipes anything. The result is `setup.openspec_untracked` and in the `project.provisioned` feed summary.

## D5 Onboarding audit
`audit: true` (existing only) makes Core create, right after the project, a ready task "Audit the existing project
<name>" for the agent named by `configs/company.yaml` `projects.audit_assignee` (default `product-manager`; the
catalog refuses an unknown agent; the create refuses with 400 when the agent is not configured). Its description
asks for the project-audit skill: read-only audit, OpenSpec baseline specs, report with options. It is the
project's first task, so its run onboards the repository. Request id `project-audit:<project>` makes a repeated
create answer the same task; `setup.audit_task_id` records it.

## D6 Agents: factory_project_create behind an exact approval
Tool `factory_project_create` (operation `factory.project.create`) with `project_id`, `name`, `description`,
`repo_mode`, `workspace`, `repo_url`, `default_branch`, `openspec`, `audit`, `request_id`. It is listed and allowed
only for agents with `communication.can_create_projects`, which defaults to true for the owner's direct reports (the
CEO; the generated company configs need no edit) and false otherwise; a tool deny still wins. The gateway always
requires an APPROVED owner approval of the exact call (like `factory_decision_record`), whatever the agent's
approvals settings: without it the call answers 423 and nothing is created; a rejected approval never authorizes.
The approved call creates the project with request id `agent:<agent>:<request_id>`, `created_by` = the agent and
`approval_id`, so a retry answers the same project and exactly one exists. The project argument is `project_id`
(not `id`, which the approval and gateway checks read as a task). `factory_task_create` gains an optional
`project_id`: the run's project (default) or a project whose `created_by` is the calling agent (403 otherwise), so
the creator can start the new project's work. Approval scoping is unchanged (the approval belongs to the run's
project and task).

## D7 Feed
`project.created` (summary: who created it, the approval when an agent did, the repository mode and when it is set
up), `project.provisioned` (node, path or URL, OpenSpec documents imported, where the untracking went),
`project.setup_failed` (node and reason). The audit task's creation is the usual `task.created`.

## Limitations
- The repository lives on the node that ran the setup; another node claiming a task later needs the same path
  (projects with `repo_url` and worktree agents clone it themselves). Moving projects between nodes is out of
  scope.
- Protocol v1 workers set up within their claim's lease like any start step; protocol v2 nodes get the start
  window extended by their inventory heartbeat (up to 15 minutes) while the setup runs.
- A project with no task never gets its repository: the setup happens with the first task's run.
