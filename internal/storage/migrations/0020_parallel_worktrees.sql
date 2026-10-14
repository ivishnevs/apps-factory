-- Parallel tasks of one project (task-worktrees D1): one writer per project goes. A worktree run holds its
-- own workspace slot (runs.workspace = '<project workspace>#task/<task>'); a run executed in the shared project
-- workspace (shared mode, or a node without worktrees) holds the project workspace path, so those still run one
-- at a time. active_task_run and session_turn stay.
DROP INDEX project_writer;
-- Nodes declare whether they run worktree tasks in their own worktree (register worktrees).
ALTER TABLE nodes ADD COLUMN worktrees INTEGER NOT NULL DEFAULT 0;
-- Pinning (task-worktrees D8, 4.1): the node reported unpushed commits or a WIP ref newer than the remote.
ALTER TABLE workspaces ADD COLUMN unpushed INTEGER NOT NULL DEFAULT 0;
