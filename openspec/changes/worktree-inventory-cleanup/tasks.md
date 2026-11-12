## 1. Protocol and Core

- [x] 1.1 Heartbeat `worktrees`, `worktrees_hash`; answer `worktrees_resend` and `removals` `{task, project, reason}`
      for nodes with feature `worktree_inventory`, `cleanup` plain IDs for others. (No `worktrees_truncated`: the node
      sends the 500 most recently used.)
- [x] 1.2 `node_worktrees` table (Postgres migration): last inventory per node, verdict, `remove_since`, error.
- [x] 1.3 Judgment (design D3) in storage, answered with the heartbeat only (not the poll); tests for every row of the table,
      including a task whose workspace moved to another node (the 2026-11-12 case) and an unknown task.
- [x] 1.4 `node.worktree_stuck` attention event after 1 h of `remove` still reported, and for `unpushed_elsewhere`;
      cleared when the entry goes.
- [x] 1.5 Keep the `workspaces.node_id` + `cleanup_listed` query for nodes without the feature.

## 2. Node

- [x] 2.1 Inventory scan of `tasks/*` (state, unpushed, last used, busy probe, hourly size cache); hash; full send on
      change and every 10 min or on `worktrees_resend`.
- [x] 2.2 Janitor fed from `removals`: de-duplicated in-flight queue, nothing kept about removed worktrees; busy →
      retry silently; removal error reported in the next inventory entry.
- [x] 2.3 `on_task_closed` hooks only for reason `closed`, once per node process (the janitor's map).
- [x] 2.4 Seal under the lock re-checks unpushed work; a `stale_copy`/`unknown_task` removal with unpushed work is
      aborted and its error reported with the entry (Core raises `node.worktree_stuck` after 1 h).
- [x] 2.5 Announce feature `worktree_inventory`.

## 3. UI

- [x] 3.1 Nodes page: worktree count, total size, removals in progress and the oldest of them (no per-node list yet).
- [x] 3.2 Feed `node.worktree_removed` (task, reason, freed size) for every removal Core answered.

## 4. Rollout and checks

- [x] 4.1 Full checks + NATS `-p 1` on a throwaway broker; storage test of the A→B case (removed from both), node
      tests of inventory, removal guards and the heartbeat exchange.
- [ ] 4.2 Deploy Core, update the Mac, hetzner-hel1 and MacBook-Air-Juliya nodes; confirm Juliya's backlog is removed
      by the first inventory heartbeat and the Nodes page shows 0 closed worktrees on all three.
