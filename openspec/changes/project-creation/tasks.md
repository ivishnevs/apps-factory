## 1. Core
- [ ] 1.1 Repository provisioning on create: local git repository (initial commit on `main`) under the node's projects root when no workspace is given; optional private GitHub repository with `repo_url` set. Verify: create without a workspace yields a project whose first task runs in a fresh repository; an existing workspace path keeps today's behaviour.
- [ ] 1.2 Creation feed event (requester, approver, repository mode).

## 2. Owner UIs
- [ ] 2.1 Web UI: New project form (id, name, description, OpenSpec, repository mode), refusals under the form; projects list updates.
- [ ] 2.2 Terminal UI: the same form.

## 3. Agents
- [ ] 3.1 `factory_project_create` behind an owner approval (`action-approvals`), allowed for the CEO by default; after approval the agent can create tasks in the new project. Verify: no project exists until approval; rejection leaves nothing; approved call creates exactly one project (request id).
- [ ] 3.2 Docs: owner guide and agent configuration (who may create projects).
