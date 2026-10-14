---
name: task-pull-request
description: Finish a code task through its pull request - submit for review, then after approval update from the default branch, resolve conflicts, merge with gh pr merge and exit done.
---

A code task runs on its own branch (`af/<task>…`) in its own worktree. The branch reaches the default branch
only through its pull request, and `factory_exit_done` is accepted only when that pull request is merged.

## Submit for review

1. Commit everything; the tree must be clean (`git status --porcelain` empty). `.factory/` is excluded.
2. Call `factory_exit_submit`. The adapter pushes the branch and opens the pull request (`gh pr create --fill`)
   if there is none; you do not push or open it yourself.
3. A `changes_requested` verdict brings the task back to you: fix, commit, submit again.

## Merge after approval

An `approved` verdict brings the task back to you, bound to the pull request and the approved head. Then:

1. Update from the default branch: `git fetch origin` and `git rebase origin/<default>` (or merge it in).
2. Resolve conflicts, then build and run the tests again. Do not merge a branch that does not build.
3. If the branch changed after approval:
   - the project policy `review_after_conflicts: required` needs a new approval: push nothing yourself, call
     `factory_exit_submit` again and stop;
   - with `agent_decides` (default), submit again when your conflict resolution was substantial (changed
     behaviour, not just a moved line); otherwise continue.
4. Push the updated branch: `git push --force-with-lease origin HEAD` (only after a rebase).
5. Merge: `gh pr merge <number> --squash --delete-branch` (any method works; squash keeps history short). Check
   with `gh pr view <number> --json state` that the state is `MERGED`.
6. Call `factory_exit_done`.

## When something fails

- `pr_not_merged` on done: the pull request is still open; finish the steps above.
- `needs_approval` on done: the head changed after approval under `review_after_conflicts: required`; submit again.
- A failed merge (checks failing, branch protection, conflicts you cannot resolve) is not a reason to exit done:
  fix what you can, otherwise exit `ask` or `block` with the reason. A failed merge counts as an attempt.
- Work that should not be merged at all ends only with an authority's `abandon_branch` answer; ask for it, do not
  close the pull request yourself.

Forge credentials (`gh auth`) live on the node; never print or copy tokens.
