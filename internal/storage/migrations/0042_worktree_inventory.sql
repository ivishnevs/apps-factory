-- worktree-inventory-cleanup: nodes report the task worktrees they hold and Core answers which to remove, so a
-- worktree is removed from every node that has it, not only from the node workspaces.node_id names.
-- worktrees_hash is the hash of the node's last full inventory (a heartbeat with only the hash keeps the rows).
-- remove_reason/remove_since: the removal Core answers for the entry and since when; stuck_raised: the one
-- node.worktree_stuck event of the entry was raised.
ALTER TABLE nodes ADD COLUMN worktrees_hash text NOT NULL DEFAULT '';
CREATE TABLE node_worktrees (
  company_id text NOT NULL,
  node_id text NOT NULL,
  project_id text NOT NULL,
  task_id text NOT NULL,
  sealed_head text NOT NULL DEFAULT '',
  unpushed bigint NOT NULL DEFAULT 0,
  busy bigint NOT NULL DEFAULT 0,
  bytes bigint NOT NULL DEFAULT 0,
  last_used bigint NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT '',
  reported_at bigint NOT NULL,
  remove_reason text NOT NULL DEFAULT '',
  remove_since bigint NOT NULL DEFAULT 0,
  stuck_raised bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, node_id, project_id, task_id)
);
