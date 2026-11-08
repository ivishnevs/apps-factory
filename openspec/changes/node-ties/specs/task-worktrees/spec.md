## MODIFIED Requirements

### Requirement: Local seal
After every terminal run the worker SHALL snapshot uncommitted changes onto a local WIP ref without changing HEAD or the
index, SHALL push that ref to the service ref `refs/factory/wip/<task>` of the task's repository after the secret scan
and the size cap, and SHALL report the sealed head and the saved WIP commit (or why it was not saved) to Core. A task
whose WIP is saved remotely SHALL NOT be tied to the node by its work; its next run on any node SHALL restore the
worktree from the remote branch and the WIP commit's files.

#### Scenario: Timeout with uncommitted work
- **WHEN** a run times out with modified and untracked files
- **THEN** the files SHALL remain in the worktree, a WIP ref SHALL contain them and be pushed to the service ref, and
  the next run SHALL continue with those files on whichever node takes it

#### Scenario: Secret in uncommitted work
- **WHEN** a run times out with a credential in an uncommitted file
- **THEN** the WIP SHALL NOT be pushed, and the task SHALL stay tied to the node by its work with the reason that the
  work could not be saved

## ADDED Requirements

### Requirement: Review runs in a disposable copy
A run of a task by an agent that is not its implementer (a reviewer) SHALL run in its own disposable worktree checked
out from the pushed head of the task branch and removed after the run. It SHALL NOT use or change the implementer's
worktree, SHALL NOT change the task's workspace record and SHALL NOT tie the task to its node.

#### Scenario: Reviewer merges main locally to test
- **WHEN** the implementer pushed the task branch from node A and a reviewer on node B merges `origin/main` into its
  copy, runs the tests and requests changes
- **THEN** the task SHALL NOT be tied to node B, and the implementer's next run on any node SHALL start from the
  remote branch without the reviewer's merge commit or files
