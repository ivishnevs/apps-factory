Status: proposed 2026-11-08 by the owner ("no manual interventions: make the system convenient and predictable").
Nothing implemented.

## Why

Game project, night of 2026-11-07: 3 nodes with 10 slots, 2 runs live, 83 open tasks, "To do 0", "Blocked 31". The
owner saw idle machines and a wall of blocked tasks and could not tell why. The answer took an hour of API reading:

1. **Every agent runs one task at a time.** `max_concurrent_runs: 0` means a limit of 1 (`tasks/state.go`). network-dev
   had four READY tasks (playtest server deploy, playtest builds, Flag Run state, mode registry) queued behind one run,
   while the owner's Mac had four free slots. Nothing said so except a per-task reason `capacity`.
2. **Planning gates that do not need to exist.** The producer made seven spec tasks (M3, M4, M5, three class-talents
   specs, second arena map) and a design task depend on two QA verifications (M2-D15, MS-4), which in turn waited on
   implementation and a serialized merge queue. Writing a spec does not need a neighbour's verification; about 25
   implementation tasks waited behind those specs. Nothing in Factory notices such a gate.
3. **A run waited on a paused node's session** (see node-readiness) while showing under To do.

Each case was "fixed" by the owner or by an outside session poking the producer. That does not scale and is not
predictable. Factory has the data to see all three by itself.

## What Changes

- **Concurrency defaults by role, visible and adjustable.** An agent's default run limit comes from its role in the
  company config (`concurrency: {developer: 2, manager: 2, default: 1}`), not a hard-coded 1; `max_concurrent_runs`
  still overrides. The team page shows each agent's limit, live runs and queued tasks.
- **Idle-capacity signal.** Core's stall sweep computes, per project, free node slots that could take queued work and
  why they do not: agent limit reached (with the agent and its queue), session affinity behind a paused or busy node,
  no ready node (node-readiness), dependency gates. When work waits while slots stay free for longer than a threshold
  (default 30 min), Core sends one deterministic "throughput" notice per episode to the project's manager (the owner
  only if the owner is the manager), naming the cause and the change that would release it ("network-dev: 4 queued,
  limit 1, 4 free slots: raise the limit or reassign"). It never changes anything itself.
- **Gate check on dependencies.** When a planning task (spec, design, plan: kind from its title or an explicit
  `kind`) depends on a verification or implementation task, the create/update answer warns `planning_gated`
  (non-blocking, like task-duplicate-warnings): "a spec rarely needs <task> done; depend on it from the implementation
  tasks instead". The throughput notice lists such gates with the number of tasks behind each.
- **Board tells the same story.** The Blocked column header splits "need you" from "waiting their turn"; a
  "Bottlenecks" strip above the board shows the top causes from the sweep (agent queues, gates, paused nodes) with
  counts. A task with a queued run shows under Running as "queued: <reason>", never under To do.
- **Manager instructions.** Default manager prompt: specs and designs depend on decisions and earlier specs, not on
  verifications; act on throughput notices (raise limits within authority, reorder, split gates).

Not in scope: automatic limit changes, automatic dependency rewrites, cost budgets (cost-ledger).

## Impact

Company config (`concurrency`), scheduler limits, stall sweep (`throughput` notice), task create/update warnings
(`planning_gated`), board and team page (web, TUI), default manager prompt. Spec: `task-engine`. Related:
`node-readiness`, `task-duplicate-warnings`, `task-board-ux`, `park-watchdog`, `session-affinity`.
