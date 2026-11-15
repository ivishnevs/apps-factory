Status: implemented 2026-11-15 after a production incident (run storm on parked tasks).

## Why

A claim ends a queued task run `stale` when the task no longer wants it: no attempt, no backoff, by design. When a
dispatcher queues a run the claim then refuses, every scheduler tick queues another one, and nothing slows it down.
On 2026-11-12 (01:47–10:14 UTC) one production company got about 40,000 such runs on three tasks parked on a
`retries_exhausted` escalation: the review dispatch path checked only `not_before`, not the parks. Each run row
stored its full run spec (about 46 KB), so the storm alone took about 1.9 GB of the database, which later filled
its volume. A smaller storm (about 2,500 runs on two tasks) happened on 2026-11-07. The review path was fixed
(7cf765f), but the class remains: any future disagreement between a dispatcher and the claim re-check repeats it.

## What Changes

- **Dispatch asks the claim's question.** `Dispatch` evaluates the task the way the claim re-check does (the run
  being queued as its live run) and queues nothing when the claim would end the run stale (a park, open
  dependencies, a released or terminal task). One decision point covers every caller, present and future.
- **A storm guard.** When a task already had `StaleRunCap` (3) runs end stale within `StaleRunWindow` (10 minutes),
  `Dispatch` queues no new run for it until the window passes, and records one feed event
  `task.dispatch_throttled` per window naming the count and the last stale reason.

Nothing changes for runs that the claim keeps (capacity, pauses and node reasons answer retry) or for `unclaimed`
runs, which the queue TTL already paces. The rule works on tasks and runs only, for every company.
