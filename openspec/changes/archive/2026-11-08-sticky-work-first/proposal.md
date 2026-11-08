Status: proposed 2026-11-08 by the owner ("the safest and most effective rule: give a node first the tasks pinned to
it or whose session it holds"). Nothing implemented.

## Why

Nodes pull work: a polling node gets the queued runs in creation order (`internal/storage/poll.go`, `ORDER BY
r.created_at`) and claims what it can. Two things tie a task to a node: **pin** (the task's unpushed work is in the
node's worktree) and **session affinity** (the node holds the agent's session). Tied runs wait for their node while it
is online, however busy; nothing makes the node take them first.

Seen on the owner's company, 2026-11-08 (UTC+1):

- 15:21: VP-3 (`e68481f0`, gameplay-dev) timed out on `hetzner-hel1`; its unpushed work pinned it there.
- 15:23 and 15:24: `hetzner-hel1` claimed two network-dev tasks any node could run (lean server image `a3ec5ba9`,
  front-end protocol `5c1dc739`) and reached 4 of 4 slots.
- VP-3 then waited for Hetzner while the Mac had 3 free slots and Juliya 2: they may not take a pinned run, and
  network-dev was at its limit with all three runs on Hetzner.
- The task said "All nodes busy: every node that can run it is full". When the pinned node is online but full, the
  verdict uses the `nodes` wait (`internal/tasks/nodes.go`, `nodesFull`) instead of `pinned_node`, so the pin is
  invisible. The Nodes page does not show what is tied to a node either.

## What Changes

- **A node gets its own work first.** When a node polls, the runs offered to it are ordered: first runs pinned to this
  node, then runs whose session this node holds, then all other runs in creation order (as today). Every existing check
  stays (limits, drivers, labels, other nodes' pins and sessions, pauses, resources); only the order changes, so no run
  becomes claimable that is not claimable today.
- **The wait names the node.** A task pinned to an online but full node waits as `pinned_node` with the node and what
  fills it ("waiting for hetzner-hel1: its unpushed work is there; the node runs 4 of 4: cinderforge, MS-2, …"), not
  as "all nodes busy". The same for a queued run waiting for its session holder.
- **Ties are visible.** The Nodes page lists per node what is tied to it: pinned tasks (unpushed work) and agent
  sessions it holds, with the runs waiting for each. A task card shows a small pin with the node name while pinned.

Considered, not in this change (discussed with the owner 2026-11-08, revisit if ties still waste capacity):
Core-side placement scoring (free slots, CPU load, labels) with a short deferral for a non-best node; pushing WIP to a
service ref at every run end so a pin dissolves; a bounded wait (e.g. 15 min) for a busy pinned node when the WIP is
pushed; a bounded wait for a busy session holder on work runs (conversations: work-and-conversation-runs); reserving
idle slots for expected tied runs (rejected: idle slots).

## Impact

Node poll ordering (`storage/poll.go`), task wait verdict for pinned and affinity waits (`tasks/nodes.go`), queued-run
reasons, Nodes page and task cards (web, TUI). Specs: `node-agent`. Related: `session-affinity`, `node-readiness`
(pause and session leases), `work-and-conversation-runs` (conversation affinity), `throughput-signals`.
