Status: proposed 2026-11-12 after a manual cleanup of orphaned worktrees on the Mac node and hetzner-hel1; waits for
the owner's approval before implementation.

## Why

Closed tasks' worktrees pile up on nodes. On 2026-11-12 the Mac node held 10 worktrees of DONE tasks (≈8.5 GB, the
oldest from 2026-11-06) and hetzner-hel1 held 5 more (≈4 GB, one CANCELLED). The janitor itself works: it removes
every worktree Core lists. Core simply never listed these.

Core keeps one `workspaces` row per task (`company_id, task_id`), and every workspace report overwrites its `node_id`
with the last node that ran the task. The cleanup list a node receives is built from that row
(`internal/storage/nodes.go`, `w.node_id=?`). A task that ran on the Mac and then had its review fix or rework on
Hetzner is listed for cleanup only to Hetzner; the Mac copy is forgotten for good. With three nodes in one company
this happens to every task that changes node, which is now the common case.

Two smaller holes make the same failure possible on a single node:

- Core lists a closed task at most `cleanupListings` (30) times. A worktree busy for the whole window (a long hook,
  a slow seal, a node that was offline when the task closed) is never listed again.
- The runner's `cleaned` map and the janitor's `closed` map live in memory; the node has no record of what it holds
  versus what Core asked it to remove.

The design hole is that cleanup is driven by Core's memory of where a worktree is, while only the node knows what
is on its disk.

## What Changes (proposed)

- **The node reports what it holds.** The heartbeat carries the node's worktree inventory: one entry per task
  worktree (task, project, sealed head, unpushed flag, last use, size). Sent in full when it changes and at least
  every 10 minutes, a hash otherwise.
- **Core judges every reported worktree** against the task and its workspace row and answers removals for this node:
  - task DONE or CANCELLED → remove;
  - task unknown to Core (deleted, other company) → remove after a 24 h grace;
  - task open, its workspace owned by another node, nothing unpushed here, unused for 24 h (or the node is below the
    low disk watermark) → remove as a stale copy;
  - otherwise keep.
- **No listing counter, no in-memory "done" set.** A removal is repeated while the node still reports the worktree;
  it ends when the worktree is gone from the inventory. Busy worktrees are retried on later heartbeats.
- **Unsafe cases are surfaced, never deleted.** A worktree with unpushed commits on a node that does not own the task's
  workspace, or one that fails removal for 1 h, raises one `node.worktree_stuck` attention event with the reason.
- **Visibility.** The Nodes page shows each node's worktree count, total size and the oldest closed one still on
  disk; read-only, no manual removal button (owner, 2026-11-12: the system cleans itself).
- Nodes without the new feature keep the current `workspaces.node_id` listing; it stays as the fallback until all
  nodes report inventory.

Out of scope: pruning local `af/*` branch refs in the bare repo (cheap, kept by the spec), Claude Code's own
`.claude/worktrees` of interactive sessions, review copies (already removed on node start).

## Impact

- Node: inventory scan of `<worktrees>/<company>/<project>/tasks/*`, heartbeat field, janitor queue fed from the
  answer, no `cleaned` map.
- Core: heartbeat/poll answer computed from the reported inventory; new `node_worktrees` table (node, task, project,
  sealed head, unpushed, bytes, last used, first failure); attention event; Nodes page columns.
- Protocol: heartbeat `worktrees` / `worktrees_hash`, answer `cleanup` items gain a `reason`; new node feature
  `worktree_inventory`.
- Specs: node-agent (janitor), node-transport (heartbeat fields).
- Rollout: after the deploy every node's first inventory heartbeat cleans what is already orphaned there,
  MacBook-Air-Juliya included (not cleaned by hand).
