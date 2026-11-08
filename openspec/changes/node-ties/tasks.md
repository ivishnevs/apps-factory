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

## 3. Verify

- [ ] 3.1 Replay VP-3: a run times out with uncommitted files on node A, node A is full, node B takes the next run and
      finds the files; with a secret in a file the task stays tied to A and says why.
