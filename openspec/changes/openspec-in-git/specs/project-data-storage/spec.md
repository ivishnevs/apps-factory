## MODIFIED Requirements

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

### Requirement: Blob transfers outlast JSON deadlines
Blob uploads and downloads (artifact blobs, run log chunks) SHALL have read and write deadlines of ten minutes, longer than the server's deadlines for JSON calls.

#### Scenario: Largest blob over a slow link
- **WHEN** a node uploads a 64 MiB artifact that takes longer than the server's JSON write timeout
- **THEN** the upload SHALL complete instead of being cut

## ADDED Requirements

### Requirement: OpenSpec lives in the project's repository
A project's OpenSpec tree SHALL be the `openspec/` directory of its git repository. Runs SHALL edit it on their task branch and it SHALL reach the default branch only through the task's pull request, reviewed with the code. Nodes SHALL NOT exclude `openspec/` from git and Core SHALL NOT sync it at exits.

#### Scenario: Rejected task with a spec edit
- **WHEN** a task edits a delta spec and its review is rejected or the task is cancelled
- **THEN** the default branch and every other task's worktree SHALL NOT contain that edit

#### Scenario: Spec as of a commit
- **WHEN** someone checks out any commit of the default branch
- **THEN** its `openspec/` SHALL be the specs that the code of that commit was reviewed against

### Requirement: Change references checked on the node
Core SHALL validate only the name of a task's `openspec_change`. Before the runtime starts, the node SHALL check the change on the task's checkout (an active `changes/<name>/proposal.md` or an archived `changes/archive/YYYY-MM-DD-<name>/proposal.md`). A missing change SHALL end the run with outcome `change_missing`, which SHALL block the task with the node's reason without counting an attempt, a backoff or the retry budget.

#### Scenario: Executor task before its proposal is merged
- **WHEN** a task linking change X is claimed while X exists only on an unmerged branch
- **THEN** the run SHALL end `change_missing` before any runtime turn and the task SHALL be blocked naming X

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

## REMOVED Requirements

### Requirement: Digest-checked write-back
**Reason**: OpenSpec edits reach the default branch through the task's pull request; there is no Core copy to write back to.
**Migration**: None (not released). Spec edits are committed on the task branch.

### Requirement: OpenSpec enabled per project with archive tasks
**Reason**: OpenSpec is a directory of the repository, not a project setting; the last task of a change archives it (see "Change state tracked through tasks").
**Migration**: None. The project setting `openspec` is removed.

### Requirement: Unsynced OpenSpec edits kept
**Reason**: Git keeps uncommitted edits in the worktree; there is no materialization to protect them from.
**Migration**: None.

### Requirement: Archive only finished changes
**Reason**: Core no longer creates archive tasks; it notifies the manager from the `done` facts instead.
**Migration**: None.
