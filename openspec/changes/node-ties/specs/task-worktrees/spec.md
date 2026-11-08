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
