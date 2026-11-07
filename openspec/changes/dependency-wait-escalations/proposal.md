Status: implemented 2026-11-07 (owner-proposed: options 2 and 3 of the analysis below, plus "a task that waits on its
dependencies goes back to waiting, never a manual block"). The threshold is a company setting (`/settings/company/stalls`), like session affinity, not a company.yaml key.

## Why

Seen on the owner's company (Gnome Arena, task M5b-P3 `9dff72b7`, 2026-11-06/07):

1. M5b-P3 depended on two planned M5 tasks in BACKLOG (they wait for the M5 spec, in roadmap order). Stall detection
   (run-recovery "Stall detection") raised a `stalled` escalation: "waits on 660f9341, which is in BACKLOG". The
   wait was intended, but the only answers are `unblock` (drop the dependency) and `cancel`: both break the plan.
2. The producer found neither right and put a **manual block** on the task to hold it ("waits on purpose for M5 …
   neither unblock nor cancel is right"). A manual block is cleared only by the owner, so the task would have stayed
   BLOCKED after M5 was done, waiting for the owner who did not know about it.
3. The owner saw the escalation and pressed `unblock`, reading it as "release the task". It removed both M5
   dependencies; only the manual block kept the task from starting before the M5 screens existed.

The escalation would have cleared by itself once the M5 tasks left BACKLOG (escalations are synced every minute and
close as "condition cleared"). Every step that hurt came from the escalation offering destructive answers to a
normal state, and from a manual block used as "keep waiting".

## What Changes

- **A planned BACKLOG dependency is not a stall at once (option 3).** A `stalled` escalation for a dependency (or
  child) in BACKLOG opens only after it has not moved for a company-configurable time (default 24h; activity: the
  dependency's status, assignee, priority or dependencies changed, or its own dependencies progressed). It goes to
  the assignee's manager, never straight to the owner unless the owner is that manager. Cancelled and unassigned
  dependencies keep escalating at once, as today.
- **Answers that do not break the plan (option 2).**
  - New answer `keep_waiting`: closes the escalation and snoozes it for this dependency until the dependency changes
    or the threshold passes again; the task keeps its dependencies.
  - `unblock` is renamed `drop_dependency` (shown as "Stop waiting for <task>"): the label says that the dependency is
    removed. The old answer name stays accepted for existing clients.
- **Waiting on dependencies is waiting, not a manual block (owner 2026-11-07).** A task that waits on open
  dependencies stays in its dependency wait (READY with reason `dependencies`: the board's Waiting column, Blocked
  after task-board-ux). The `block` action (`factory_task_block`, `POST /tasks/{id}/actions`) by an agent is refused
  for a task with open dependencies when the reason is only that wait, with the hint "it already waits on its
  dependencies; answer the stalled escalation with keep_waiting". The agent instructions for managers say the same.
  To hold a task for an unplanned reason an agent still blocks it; to make it wait for work it adds a dependency.

- **Two prod crashes of 2026-11-07 (bugs, fixed here).**
  - *A reopened escalation collides with its closed question.* The `stalled` escalation on M5b-P3 reopened with the
    same words and request id as the question answered at 02:03; its fingerprint hit the `unique_question` index and
    the insert failed. The fingerprint of a Factory question includes the opening (escalation episode), so a
    reopened escalation asks a new question; a collision on a Factory question is treated as "already asked", never
    as an error.
  - *One failing sweep stops Core for every company.* `maintain` returns the first error of any step
    (`sweepStalls`, `checkUnmerged`, cost pricing), the tick fails and Core exits; on prod it restarted every ~5 s
    for all tenants because of one task. Background sweeps log the error with company and task, raise a
    `sweep_failed` alert once per episode and go on with the next task and company; only storage loss stays fatal.
    Hand fix on prod: the closed questions of that task got a distinct fingerprint.

## Impact

- `internal/core/stalls.go` (threshold, activity, target), `internal/storage/escalations.go` (answers `keep_waiting`,
  `drop_dependency`, snooze), `internal/storage/tasks.go` / `internal/core/tasks.go` (refused agent block),
  `internal/factorytools` descriptions, the manager instructions, company config (`stalls.backlog_after`), web and TUI
  escalation answers, `internal/storage/messages.go` (question fingerprint), `internal/core/core.go` (`maintain`
  error isolation).
- Specs: `run-recovery` (Stall detection), `task-engine` (agent block on a dependency wait).
- Related: `task-board-ux` (Waiting renamed Blocked), `park-watchdog` (parks without activity).

Not in this change: who clears a manual block set by an agent (today only the owner); revisit if agents' blocks keep
landing on the owner.
