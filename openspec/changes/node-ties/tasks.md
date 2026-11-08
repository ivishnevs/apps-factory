## 1. One notion

- [ ] 1.1 `tie: {node, reason: work|session, since, release}` on task views and queued-run reasons; old reason and
      escalation names kept as aliases for one release; tests.
- [ ] 1.2 Web, TUI, inbox escalation and docs say "tied to <node>" with the reason and how it ends.

## 2. Unsaved work saved remotely

- [ ] 2.1 Node: after the local seal, push the WIP ref to `refs/factory/wip/<task>` through the secret scan and the
      size cap; report the saved WIP commit or why it was not saved; tests.
- [ ] 2.2 Core: a task with its WIP saved remotely is not tied by work; the tie reason names a failed save.
- [ ] 2.3 Restore on another node: worktree from the remote branch plus the WIP commit's files; tests (timeout on one
      node, next run on another with the same files).
- [ ] 2.4 Delete the service ref when the task closes or its branch merges.

## 3. Review runs in a disposable copy

- [ ] 3.1 Node: a run of an agent that is not the task's implementer gets a disposable worktree from the pushed head
      of the task branch, removed after the run; the implementer's worktree is not touched; tests.
- [ ] 3.2 Core: the workspace report of such a run does not change the task's workspace record or tie; tests.
- [ ] 3.3 A reviewer's push to the task branch from the disposable copy goes through the clean-tree and secret-scan
      rules; the implementer's next run starts from the remote branch.

## 4. Verify

- [ ] 4.1 Replay VP-3: a run times out with uncommitted files on node A, node A is full, node B takes the next run and
      finds the files; with a secret in a file the task stays tied to A and says why.
- [ ] 4.2 Replay `85622aeb`: the implementer pushes on node A, a reviewer on node B merges origin/main locally and
      requests changes; the task is not tied to B, the next implementer run goes to any free node and its worktree has
      no reviewer commits.
