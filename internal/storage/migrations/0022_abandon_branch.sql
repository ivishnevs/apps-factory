-- abandon_branch (explicit-exits-authority D4, task-worktrees 4.3): an authority decided the task's branch is
-- not merged; done is accepted without a merged pull request and the unmerged-branch check skips the task.
ALTER TABLE workspaces ADD COLUMN abandon_branch INTEGER NOT NULL DEFAULT 0;
