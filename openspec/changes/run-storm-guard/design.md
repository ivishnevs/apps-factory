## D1. Dispatch shares the claim's verdict

`recheckTask` (claim) and `Dispatch` call one function, `unwantedTx`: the task's snapshot with the run as its live
run and the assignee as a configured agent, evaluated by `tasks.Evaluate`; `StaleVerdict` says whether the claim
would end the run stale. Dispatch answers `ErrConflict` (the dispatcher skips the task this tick) instead of
inserting the run. The per-path checks in the dispatchers stay (they avoid queueing work early), but correctness
no longer depends on them.

## D2. Storm guard per task

Counted from `runs` (`outcome='stale'`, `created_at` within the window), so it needs no new state and survives
restarts. Cap 3 in 10 minutes: legitimate stale ends (a reassignment, a cancel or a park between dispatch and
claim) are rare and single; three in ten minutes means the dispatcher and the claim disagree. The refusal is a
conflict, not a block: the task runs again by itself after the window. The feed event is written once per window
(checked against the task's `feed_events`), so a stuck task shows on its timeline without flooding it.

## Limits

The guard bounds a storm to a few runs per task per window; it does not fix the disagreement that causes it. The
feed event is how a new disagreement is found.
