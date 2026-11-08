Status: proposed 2026-11-08 by the owner ("pins and session affinity are two mechanisms; make them one notion so it
is not confusing"). Nothing implemented.

## Why

Two mechanisms tie a task to a node, with different words everywhere:

| | Pin (task-worktrees) | Session affinity (session-affinity) |
|---|---|---|
| Holds | the task's unpushed work in the node's worktree | the agent's model session on the node |
| Moving it loses | the work | only the agent's context (it rebuilds from Factory) |
| Released | node offline past the pin timeout, then an owner escalation (`pinned_node_unavailable`, `reset_workspace`) | automatically, 90 min after the node goes inactive |
| Shown as | `pinned_node_unavailable`, "unsaved work is on node", `pinned` waits, `unpushed` | `affinity` queued reason, "holds this agent's session", session locks |

On 2026-11-08 the owner asked whether "sticking" and "pins" were the same thing. sticky-work-first already treats
both as one (offers, the Nodes page "Tied to this node"), but tasks, queued runs, escalations, the API and docs still
use two vocabularies.

Pins also arise more often than they need to. After every terminal run the node seals uncommitted work onto a
**local** WIP ref (task-worktrees "Local seal": "SHALL NOT push it"), so any run that ends without an exit (timeout,
crash, failure) leaves the task pinned. VP-3 (`e68481f0`) timed out on `hetzner-hel1` and could then run only there
while the Mac sat idle.

## What Changes

- **One notion: a tie.** A task or a queued run may be *tied* to a node, with a reason:
  - `work`: the task's unsaved work is only on that node (today's pin);
  - `session`: the agent's session is on that node (today's affinity).
  The task view and queued-run reasons carry `tie: {node, reason, since, release}` where `release` says how it ends
  ("when the work is saved remotely or after the owner resets the workspace" / "90 min after the node goes inactive").
  The web and TUI say "tied to <node>: its unsaved work is there" or "…: its agent session is there" everywhere (board,
  task, Nodes page, inbox escalation), and the docs use the same words. Existing API reasons and escalation names stay
  as aliases for one release (`pinned_node_unavailable` → tie `work`, `affinity` → tie `session`).
- **Release rules stay different.** Losing a work tie loses work, so it is released only by saving the work remotely or
  by the owner; a session tie is released automatically (work runs: session-affinity timeout; conversations: 10 min,
  work-and-conversation-runs).
- **Unsaved work is saved remotely, so work ties become rare.** After the local seal, the node pushes the WIP ref to a
  service ref of the task's repository, `refs/factory/wip/<task>` (not a branch: no CI, no PR noise), through the same
  secret scan as every push. Core records the saved WIP commit; the task is then not tied by work, and a run on another
  node restores the worktree from the remote branch plus that WIP commit (files, not commits, as the local seal does).
  A work tie remains only while the push fails (no network, no repository access, a secret finding), and the tie's
  reason says so. The service ref is deleted when the task closes or its branch is merged.

Not in scope: changing session-affinity timeouts; placement scoring; moving a session between nodes.

## Open questions (with recommendations)

1. **Secret finding in WIP.** Refuse the WIP push and keep the work tie (recommended; same rule as other pushes), or
   push a redacted snapshot? Recommended: refuse, and the task's tie says "unsaved work could not be saved: secret
   finding in <file>".
2. **Large or binary files in WIP** (renders, captures). Recommended: the push follows the repository's `.gitignore`
   (the seal already does) and a per-file size cap (e.g. 50 MB); a larger file keeps the work tie with that reason.
3. **Alias period.** Keep the old reason and escalation names for one release, then drop them (recommended), or keep
   them for good in the API.

## Impact

Task verdicts and queued-run reasons (`tie`), escalation texts, web and TUI wording, docs; node seal pushes the WIP ref
with the secret scan; Core records the saved WIP and treats such a task as untied; worktree restore on another node
from branch + WIP commit; cleanup of service refs. Specs: `task-worktrees` (Local seal becomes Saved seal),
`node-agent`. Related: sticky-work-first (archived 2026-11-08), `session-affinity`, `work-and-conversation-runs`,
`node-readiness`.
