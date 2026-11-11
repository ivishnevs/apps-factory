Status: proposed 2026-11-06 by the owner; owner decided 2026-11-06: the lock is released only when its node is
inactive beyond a timeout, never because the node is busy; release timeout 90 minutes (company setting). Implemented 2026-11-07 (3.2, the before/after count, is a measurement left to do).

## Why

A run continues its agent's session: the session key is (task, agent), so rework rounds and re-reviews resume the
same session, and every message of a conversation resumes one session. Claude Code and Codex keep sessions on the
machine that ran them. With a second node (2026-11-06: MacBook-Air-Juliya next to the owner's Mac) any node may
claim any queued run, first come first served. When a continuation lands on another node, the node correctly starts
a fresh session ("the previous session of this task is not available here; take the context from Core"), but the
agent loses its working memory, and Core then records the new session: if the next run lands back on the first
node, it starts fresh again. A conversation can lose its memory on every hop (the game designer's conversation moved
to the second node on 2026-11-06).

Today only unpushed work pins a task to a node (task-worktrees: a hard pin, released by the owner's
`reset-workspace` answer when the node is unavailable). Pushed work and conversations have no preference at all.

## What Changes

- **Session affinity.** A run that continues a session (a task's (task, agent) session or a conversation) prefers the
  node that holds that session. Core records the session's node with the session (the node of the run that produced
  it). While the affinity holds, other nodes do not get the run in their offers and a claim from them is refused
  with retry `affinity`; the holder takes it when it has a free slot.
- **Released only when the holder is inactive, after a timeout (owner 2026-11-06).** The first node that claims
  an agent's run on a task locks that (agent, task) pair — and a conversation — to itself. Every later run of the
  pair (rework, re-review, the next message) can be taken only by that node while it is active, however long it is
  busy: a busy node is waited for, the lock is not released for it. When the node becomes inactive (offline, its
  heartbeat lost, paused, removed, or no longer able to run the agent), a release timer starts. If the node is active
  again before the timer ends, the timer is cleared and the lock stays. If it is still inactive when the timer ends,
  the lock is released: any capable node may take the run, starts a fresh session with the existing note, and the
  lock moves to it.
- **Two strengths, named like Kubernetes node affinity.** The existing pin of unpushed work stays *required*
  (released only by the owner's `reset-workspace`, since the work exists only on that node); session affinity is
  *preferred* in that it is released automatically after the inactivity timeout. Both are shown on the task: "Waiting for mac-main, it holds this
  agent's session (up to 8 min more)" / "Its unpushed work is on mac-main".
- **New sessions go anywhere.** A run without a session to continue (a task's first run, a new agent on it, a
  reset) has no affinity.
- **Visible and countable.** The run records whether it resumed or started fresh and why (another node, affinity
  released: offline / waited too long / reset), so fresh starts can be seen and tuned.

Naming: "session affinity" (preferred) next to the existing "pin" (required); alternatives considered: sticky
session, node lease.

## Impact

Core: session's node recorded with the run's session, offers and claim filtered by affinity with release rules,
reason `affinity` in the task verdict, inactivity timer per lock, company setting for the release timeout. Node: nothing new (it already reports whether
it resumed). Web and TUI: the reason on the task, resumed/fresh on runs. Specs: `node-transport` (offers and claim).
Related: `task-worktrees` (required pin), `node-runtime-accounts` (sessions belong to a node's runtime login),
`agent-context-window`.

## Implementation (2026-11-07)

- The session's node is read from `runs.node_id` of the session's latest other run (no new column: 2.1 needed no
  migration of its own); `session_locks` keeps the inactivity timers, `companies.session_affinity_minutes` the
  timeout (default 90).
- Offers skip runs whose session an active other node holds; the claim refuses them with retry `affinity`; the
  queued reason `affinity` names the holder and, when it is inactive, the release time (`until`).
- Decided during implementation: an offline holder's timer starts at its last heartbeat and a paused one's at the
  pause, so the 90 minutes count from when the node actually became inactive; a company-wide pause does not count as
  the holder's inactivity (every node is paused alike).
