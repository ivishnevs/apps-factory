-- workspace_diverged (task-worktrees 2.3): a reset_workspace answer asks the task's node to reset its
-- worktree from the remote branch (heartbeat answer reset); the task runs again once the node confirms.
ALTER TABLE workspaces ADD COLUMN reset_pending INTEGER NOT NULL DEFAULT 0;
