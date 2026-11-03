# project-data-storage Specification

## Purpose
Keep project data owned by Core, materialize it for runs and never read node disks.

## Requirements

### Requirement: Core never reads node disks
Core SHALL store artifact contents, report attachments and large run logs in its own storage and SHALL NOT access files on nodes. Code and OpenSpec documents SHALL remain in the project's git repository; Core SHALL hold no repository clones, no forge credentials and no OpenSpec documents.

#### Scenario: Core on another machine
- **WHEN** Core runs on a host that shares no filesystem with any node
- **THEN** change checks (on the node), artifact approvals and claims SHALL work unchanged

### Requirement: Materialization for runs
The claim response SHALL list the run's pinned approved artifact inputs by digest, and the node SHALL place them under `.factory/` in the worktree before the runtime starts. OpenSpec documents SHALL NOT be materialized: the run reads them from its checkout.

#### Scenario: Missing content
- **WHEN** a listed blob cannot be downloaded
- **THEN** the run SHALL end as `setup_failed` before the runtime starts

### Requirement: Content-bound artifact versions
Uploaded artifact versions SHALL record their content digest, and approval SHALL bind to that digest.

#### Scenario: Same version label, different content
- **WHEN** an artifact version is approved and a different file is later uploaded under a new version
- **THEN** the approval SHALL NOT apply to the new version

### Requirement: Run logs uploaded redacted
Nodes SHALL upload run logs and report attachments to Core storage through the API, and SHALL replace known credential values before upload.

#### Scenario: Token printed by a tool
- **WHEN** a command in a run prints a forge token that the node holds
- **THEN** the uploaded log SHALL contain a redaction marker instead of the token

### Requirement: Blob transfers outlast JSON deadlines
Blob uploads and downloads (artifact blobs, run log chunks) SHALL have read and write deadlines of ten minutes, longer than the server's deadlines for JSON calls.

#### Scenario: Largest blob over a slow link
- **WHEN** a node uploads a 64 MiB artifact that takes longer than the server's JSON write timeout
- **THEN** the upload SHALL complete instead of being cut

### Requirement: OpenSpec lives in the project's repository
A project's OpenSpec tree SHALL be the `openspec/` directory of its git repository. Runs SHALL edit it on their task branch and it SHALL reach the default branch only through the task's pull request, reviewed with the code. Nodes SHALL NOT exclude `openspec/` from git and Core SHALL NOT sync it at exits.

#### Scenario: Rejected task with a spec edit
- **WHEN** a task edits a delta spec and its review is rejected or the task is cancelled
- **THEN** the default branch and every other task's worktree SHALL NOT contain that edit

#### Scenario: Spec as of a commit
- **WHEN** someone checks out any commit of the default branch
- **THEN** its `openspec/` SHALL be the specs that the code of that commit was reviewed against

### Requirement: Change references checked on the node
Core SHALL validate only the name of a task's `openspec_change`. Before the runtime starts, the node SHALL check the change on the task's checkout (an active `changes/<name>/proposal.md` or an archived `changes/archive/YYYY-MM-DD-<name>/proposal.md`). A missing change SHALL end the run with outcome `change_missing`, which SHALL hold the task with a `change_missing` escalation (answers retry and cancel) to the assignee's manager, naming the node's reason, without counting an attempt, a backoff or the retry budget.

#### Scenario: Executor task before its proposal is merged
- **WHEN** a task linking change X is claimed while X exists only on an unmerged branch
- **THEN** the run SHALL end `change_missing` before any runtime turn and the task SHALL be held by an escalation naming X

#### Scenario: Archived change in review
- **WHEN** the last task of change X archived it in its pull request and its review run is claimed
- **THEN** the node preflight SHALL accept the archived proposal

### Requirement: Change state tracked through tasks
Core SHALL know a project's OpenSpec changes from the tasks that link them and SHALL NOT store their documents. A `done` exit of a task linking a change SHALL carry facts computed on the checkout after the merge: the change's state (`active`, `archived` or `missing`) and the number of unchecked items of its `tasks.md`. When the last open task linking a change becomes DONE and the reported state is `active`, Core SHALL post one feed event to the project's manager: unchecked work without a task, or a finished change that was not archived. A change linked by no open task and by no task closed within the orphan window SHALL be reported to the manager once.

#### Scenario: Last task forgot to archive
- **WHEN** the last open task of change X becomes DONE and its facts report X `active` with no unchecked item
- **THEN** the manager SHALL get one feed event that X was not archived

#### Scenario: Work left without tasks
- **WHEN** the last open task of change X becomes DONE and its facts report X `active` with 3 unchecked items
- **THEN** the manager SHALL get one feed event that X has 3 unchecked items and no task works on them

#### Scenario: Archived in the last pull request
- **WHEN** the last open task of change X becomes DONE and its facts report X `archived`
- **THEN** Core SHALL post no event and create no task
