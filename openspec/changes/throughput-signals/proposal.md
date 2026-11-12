Status: proposed 2026-11-08 by the owner ("no manual interventions: make the system convenient and predictable").
Scope settled with the owner 2026-11-12: one generic idle-capacity detector instead of a fix per case; no open
questions. Implemented 2026-11-12 (migration 0037; replays with fakes; see tasks.md). The owner alert is an
ordinary task alert of kind `throughput`; the "with <manager>" mark in the Inbox comes from inbox-badge-parity.

## Why

Game project, night of 2026-11-07: 3 nodes with 10 slots, 2 runs live, 83 open tasks, "To do 0", "Blocked 31". The
owner saw idle machines and a wall of blocked tasks and could not tell why. The answer took an hour of API reading:

1. **Every agent runs one task at a time.** `max_concurrent_runs: 0` means a limit of 1 (`tasks/state.go`). network-dev
   had four READY tasks queued behind one run while the owner's Mac had four free slots. Nothing said so except a
   per-task reason `capacity`.
2. **Planning gates that do not need to exist.** The producer made seven spec tasks and a design task depend on two QA
   verifications, which waited on implementation and a serialized merge queue; about 25 implementation tasks waited
   behind those specs. Nothing in Factory noticed such a gate.
3. **A run waited on a paused node's session** (see node-readiness) while showing under To do.

The morning of 2026-11-08 added four more shapes (a merge queue held in the producer's messages, tasks waking on a
timer for a message, a manager not knowing what a node can do, an owner question hidden among blocks), and on
2026-11-12 seven runs waited on `hetzner-hel1` paused for a critical disk because the session lock did not count that
pause (fixed in code the same day). Each case was found by hand. Fixing them one by one does not scale: Factory
already knows, for every queued run, why each node does not take it (`queuedNodes`), so it can find and explain any
idle capacity itself.

## What Changes

- **Idle-capacity sweep (the core).** Core's stall sweep compares, per project, queued work with free slots, using the
  per-run, per-node reasons Core already computes for queued runs (owner pause, critical disk, offline, driver,
  labels, readiness, slots, resources, session or work tie, agent limit) and the dependency graph (a task with N tasks
  behind it). No separate cause logic: a new cause added to the queued reasons is picked up by the sweep without
  change. When work waits while capacity is idle or unusable for longer than a threshold (default 30 min, company
  setting), Core sends one deterministic "throughput" notice per episode to the project's manager and raises an owner
  alert marked "with <manager>" (inbox-badge-parity), naming the cause and what would release it: "7 runs wait for
  hetzner-hel1: paused, disk 4.9 GiB free (critical 5 GiB); free disk or Release now", "network-dev: 4 queued, limit 1,
  4 free slots: raise the limit or reassign", "7 tasks wait behind MS-4 (verification)". It never changes anything.
- **Concurrency defaults by role.** An agent's default work-run limit comes from its role in the company config
  (`concurrency: {developer: 2, manager: 2, default: 1}`), not a hard-coded 1; `max_concurrent_runs` still overrides
  (work-and-conversation-runs: role defaults apply to work runs only). The team page shows each agent's limit, live
  runs and queue.
- **Exclusive resources per task.** A task may declare the node resources its runs need (`resources: {mac-build: 1}`),
  added to the agent's; Core already enforces node `resources` at claim. A queue such as "one integration gate on the
  Mac at a time" is then held by Core and visible as `resource_busy`, not in the manager's messages.
- **Board tells the same story.** A "Bottlenecks" strip above the board shows the sweep's top causes with counts; the
  Blocked column splits "need you" from "waiting their turn"; a task with a queued run shows under In progress as
  "queued: <reason>", never under To do.
- **Manager instructions.** Default manager prompt: specs and designs depend on decisions and earlier specs, not on
  verifications; act on throughput notices (raise limits within authority, reorder, split gates); use task resources
  for exclusive work instead of announcing turns.

Not in scope: automatic limit changes, automatic dependency rewrites, cost budgets (cost-ledger).

## Settled 2026-11-12 (owner)

- **No `planning_gated` warning.** Guessing a planning task from its title is a heuristic; the sweep names real gates
  deterministically from the dependency graph and the count of tasks behind them.
- **The 2026-11-08 cases go where they have a general fix:**
  1. Merge queue held in messages: exclusive resources per task (above).
  2. Tasks waiting for a message on a timer: decision-waits, widened to waiting on an event (a decision, a message
     from a named agent or the owner, promised owner details).
  3. Manager not knowing what a node can do: node-packaging-updates, nodes detect and report their labels.
  4. Owner question among blocks: inbox-badge-parity, agent-questions-in-chat and the Blocked split (above).
- **Who hears it:** the project's manager gets the notice; the owner sees the same alert in the Inbox marked
  "with <manager>".

## Impact

Stall sweep (`throughput` notice and alert over the queued reasons), company config (`concurrency`, throughput
threshold), task `resources`, board and team page (web, TUI), default manager prompt. Spec: `task-engine`. Related:
`node-readiness`, `inbox-badge-parity`, `decision-waits`, `node-packaging-updates`, `task-board-ux`, `park-watchdog`,
`session-affinity`.
