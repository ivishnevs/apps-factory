Status: idea (not scheduled; the owner wants to think about it). Nothing here is implemented.

## Why

A parked task can sit unnoticed for a long time when the thing it waits on stops moving without becoming
"stuck" by the existing rules: children that are assigned and released but never run or finish, an ask whose
recipient keeps not answering inside its deadline, a block on a task that idles. Stall detection catches only
BACKLOG, unassigned or cancelled dependencies and children; the `idle` alert covers `why = idle`, and
`no_progress` covers tasks whose runs fail to progress. A parked task has no runs, so none of them fires.

## What Changes

- A general park watchdog in Core's stall sweep: when a task has been parked (children, ask, block, escalation
  awaiting a manager — not a wait with a deadline, which has its own timer, and not an ask locked to the owner,
  which the owner's inbox already shows) longer than a company-configurable threshold (default 24h) with no
  activity — no new trigger on the task, no run or status change of its children or of the tasks it waits on —
  Core opens one self-clearing escalation per episode to the assignee's manager (the owner at the top),
  naming the park and what it waits on.
- Activity or the park clearing ends the episode; the escalation clears itself like `stalled`.
- Threshold in `configs/company.yaml` (e.g. `stalls.park_after: 24h`), validated with a minimum.
- Complements the `idle` and `no_progress` alerts; no change to their behaviour.

## Impact

- `internal/core/stalls.go` (sweep), `internal/storage` (activity timestamp per park: latest trigger, latest
  child or dependency change), company config schema, `docs/task-lifecycle.md` stall section.
- New escalation reason (e.g. `parked_too_long`) with answers (`unblock`, `cancel`, maybe `extend`).
- Open questions: what counts as activity for an ask (a forward up the chain?), per-agent thresholds, whether
  the owner should see it when the manager is the agent that the task waits on.
