## 1. Protocol and Core

- [ ] 1.1 Heartbeat `worktrees`, `worktrees_hash`, `worktrees_truncated`; answer `worktrees_resend`; `cleanup` items
      `{task, project, reason}` for nodes with feature `worktree_inventory`, plain IDs for others.
- [ ] 1.2 `node_worktrees` table (Postgres migration): last inventory per node, verdict, `remove_since`, error.
- [ ] 1.3 Judgment (design D3) in storage, same code for heartbeat and poll answers; tests for every row of the table,
      including a task whose workspace moved to another node (the 2026-11-12 case) and an unknown task.
- [ ] 1.4 `node.worktree_stuck` attention event after 1 h of `remove` still reported, and for `unpushed_elsewhere`;
      cleared when the entry goes.
- [ ] 1.5 Keep the `workspaces.node_id` + `cleanup_listed` query for nodes without the feature.

## 2. Node

- [ ] 2.1 Inventory scan of `tasks/*` (state, unpushed, last used, busy probe, hourly size cache); hash; full send on
      change and every 10 min or on `worktrees_resend`.
- [ ] 2.2 Janitor fed from cleanup items: de-duplicated in-flight queue, no `cleaned`/`closed` maps; busy → retry
      silently; removal error reported in the next inventory entry.
- [ ] 2.3 `on_task_closed` hooks only for reason `closed`, once per worktree via `.factory/closed_hooks_done`.
- [ ] 2.4 Seal under the lock re-checks unpushed work; a `stale_copy`/`unknown_task` removal with unpushed work is
      aborted and reported as `unpushed_elsewhere`.
- [ ] 2.5 Announce feature `worktree_inventory`.

## 3. UI

- [ ] 3.1 Nodes page: worktree count, total size, oldest closed worktree; per-node list with verdicts.
- [ ] 3.2 Activity feed `worktree.removed`.

## 4. Rollout and checks

- [ ] 4.1 Full checks + NATS `-p 1` on a throwaway broker; e2e: task runs on node A, review fix on node B, task DONE →
      both worktrees removed.
- [ ] 4.2 Deploy Core, update the Mac, hetzner-hel1 and MacBook-Air-Juliya nodes; confirm Juliya's backlog is removed
      by the first inventory heartbeat and the Nodes page shows 0 closed worktrees on all three.
