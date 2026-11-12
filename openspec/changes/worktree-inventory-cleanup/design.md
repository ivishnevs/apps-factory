## Context

Worktree lifecycle today (task-worktrees, node-agent D4):

1. A run of a task on node N creates or reuses `<root>/<company>/<project>/tasks/<task>`; at run end the node seals
   it and reports the workspace (`POST /runs/{id}/workspace`), which upserts the single `workspaces` row of the task
   with `node_id = N`.
2. When the task becomes DONE or CANCELLED, every heartbeat answer to the node in `workspaces.node_id` lists the task
   under `cleanup` until `cleanup_listed` reaches 30.
3. The node janitor locks the worktree, stops a leftover run group, seals, runs `on_task_closed` hooks, removes the
   worktree and its scratch directory; a busy worktree is skipped and waits for the next listing.

Observed on 2026-11-12: 15 orphaned worktrees on two nodes, all with `workspaces.node_id` pointing at the *other*
node. The janitor logged no attempt for any of them.

## Goals / Non-Goals

Goals:
- Every worktree of a closed task is removed from every node that holds it, within one heartbeat interval of the node
  being online and the worktree idle.
- A node that was offline, busy or restarted catches up without Core remembering anything about it.
- Never delete work that exists only on that disk.

Non-goals:
- Moving unpushed work between nodes (node-ties does that).
- Cleaning the bare repo, caches or conversation directories (prune hooks do that).

## Decisions

### D1. The node's disk is the source of truth; Core only judges

The node scans its worktrees root and reports what it has. Core never keeps "where worktrees are" as authoritative
state; it keeps the last reported inventory per node only for judgment and the Nodes page.

Alternative considered: key `workspaces` by (task, node) and list cleanup per row. Rejected: it fixes the multi-node
case but not the worktrees Core never heard of (a report lost on a crash, a reinstall that reused the directory,
tasks deleted from Core), and it still needs a counter or an ack to stop listing.

### D2. Inventory in the heartbeat, sent by hash

```json
"worktrees_hash": "sha256 of the sorted entries",
"worktrees": [
  {"task": "5c1d…", "project": "gnome-arena", "sealed_head": "99f6…", "unpushed": false,
   "last_used": 1794167204, "bytes": 887000000, "busy": false}
]
```

- Scan: glob `tasks/*` under each company/project the node serves, read `.factory/state` (sealed head, saved WIP),
  `git status`/`rev-list @{u}..` for unpushed, mtime of `.factory/state` for `last_used`, a cached `du` refreshed at
  most hourly for `bytes`, a non-blocking flock probe for `busy`.
- The full list is sent when the hash changes, and at least every 10 minutes (Core may have lost it on a redeploy);
  otherwise only the hash. Core answers `worktrees_resend: true` when it has no inventory for that hash.
- Bounded: at most 500 entries; above that the oldest 500 by `last_used` go and `worktrees_truncated: true` is set.

### D3. Judgment rules (Core)

For each reported entry, against `tasks` and `workspaces`:

| Task state | Workspace owner | Entry | Verdict |
|---|---|---|---|
| DONE / CANCELLED | any | any | `remove` (reason `closed`) |
| not found | — | unused ≥ 24 h | `remove` (reason `unknown_task`) |
| open | this node | — | keep |
| open | other node or none | `unpushed=false`, unused ≥ 24 h or node disk `low` | `remove` (reason `stale_copy`) |
| open | other node | `unpushed=true` | keep, `node.worktree_stuck` (reason `unpushed_elsewhere`) |

`remove` items go out in the existing `cleanup` field as objects `{task, project, reason}`; old nodes keep getting
plain task IDs from the current query (D6).

`stale_copy` is safe because a later run on this node either reuses the directory after the existing
workspace_diverged reset or recreates it from the remote branch; the remote branch already holds everything that was
pushed, and `unpushed=false` means nothing else exists.

### D4. Idempotent removal, no counters

A removal is answered on every heartbeat for as long as the node reports the entry; the janitor de-duplicates its
in-flight queue but keeps no "done" set. A removal ends when the entry disappears from the inventory. `cleanup_listed`
is no longer read for inventory nodes.

The janitor treats `busy` as "try later" without logging per heartbeat. When an entry has been answered `remove` for
1 h and is still reported, Core raises one `node.worktree_stuck` attention event (reason: busy for 1 h, seal failed,
removal error the node reported in `worktrees[].error`) and clears it when the entry goes.

### D5. Hooks and seal stay where they are

`on_task_closed` hooks run only for reason `closed`, once per node per task (as today, but keyed on the removal, not
on an in-memory map: a hook marker file `.factory/closed_hooks_done` inside the worktree survives a node restart and
goes with the worktree). The seal before removal stays; a seal that finds unpushed work turns the removal into
`unpushed_elsewhere` reported back instead of deleting.

### D6. Compatibility and rollout

- New node feature `worktree_inventory`. Core uses D3 for nodes that announce it and the current
  `workspaces.node_id` + counter query for others.
- Core deploys first (accepts and ignores the field from old Core's point of view: unknown JSON fields are already
  ignored), then nodes update through the normal node update channel.
- First inventory heartbeat of each updated node cleans its backlog; the janitor removes one worktree at a time, so a
  large backlog does not stall runs.

### D7. Visibility

`node_worktrees` (company, node, task, project, sealed_head, unpushed, bytes, last_used, verdict, remove_since,
error) feeds the Nodes page: "Worktrees: 3 (2.6 GB) · oldest closed: 2 h", and a per-node list behind it. The
activity feed gets `worktree.removed {task, node, reason, bytes}`.

## Risks / Trade-offs

- Scan cost on every heartbeat → hash and hourly `du`; the glob and state reads are milliseconds for hundreds of
  entries.
- A wrong `unpushed=false` would lose work in `stale_copy` → the seal before removal re-checks under the lock and
  aborts on unpushed commits (D5); `stale_copy` also requires 24 h without use.
- Core redeploy loses nothing: inventory is re-sent within 10 minutes and the verdict is recomputed.

## Decisions by the owner

- 2026-11-12: no manual "remove now" button on the Nodes page; the system cleans worktrees itself. A stuck
  worktree is only surfaced by the `node.worktree_stuck` attention event, and the fix goes into the system.

## Open Questions

- 24 h for `stale_copy` and `unknown_task`: fine as defaults, or make them company settings?
