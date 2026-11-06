Status: proposed 2026-11-06 by the owner (do not split an agent's session across nodes; release it when its node
is gone). Nothing implemented.

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
- **Released when the holder cannot take it.** The affinity is released, and any capable node may take the run
  (starting a fresh session with the existing note, and the affinity moves to that node), when the holder is:
  offline beyond the node offline timeout, removed, paused, no longer able to run the agent (driver, labels), or
  when the run has waited for it longer than the affinity wait (default 10 minutes; per company setting, owner to
  confirm). A busy holder is waited for up to that time — a short wait keeps the session, a long one costs more than
  a fresh start.
- **Two strengths, named like Kubernetes node affinity.** The existing pin of unpushed work stays *required*
  (released only by the owner's `reset-workspace`, since the work exists only on that node); session affinity is
  *preferred* (released automatically as above). Both are shown on the task: "Waiting for mac-main, it holds this
  agent's session (up to 8 min more)" / "Its unpushed work is on mac-main".
- **New sessions go anywhere.** A run without a session to continue (a task's first run, a new agent on it, a
  reset) has no affinity.
- **Visible and countable.** The run records whether it resumed or started fresh and why (another node, affinity
  released: offline / waited too long / reset), so fresh starts can be seen and tuned.

Naming: "session affinity" (preferred) next to the existing "pin" (required); alternatives considered: sticky
session, node lease.

## Impact

Core: session's node recorded with the run's session, offers and claim filtered by affinity with release rules,
reason `affinity` in the task verdict, company setting for the wait. Node: nothing new (it already reports whether
it resumed). Web and TUI: the reason on the task, resumed/fresh on runs. Specs: `node-transport` (offers and claim).
Related: `task-worktrees` (required pin), `node-runtime-accounts` (sessions belong to a node's runtime login),
`agent-context-window`.
