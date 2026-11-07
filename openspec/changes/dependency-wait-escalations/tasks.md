## 1. Stall detection

- [ ] 1.1 `stalls.backlog_after` in the company config (default 24h, validated minimum); a BACKLOG dependency or child
      escalates only after it has not changed for that long; cancelled and unassigned dependencies as today.
- [ ] 1.2 Target: the assignee's manager (owner only at the top) for every `stalled` escalation; tests.

## 2. Answers

- [ ] 2.1 `keep_waiting`: close the escalation and snooze (task, dependency) until the dependency changes or the
      threshold passes again; tests including a resync that must not reopen it.
- [ ] 2.2 `drop_dependency` replaces `unblock` (old name accepted); comment wording; web and TUI labels
      ("Stop waiting for <task>", "Keep waiting").

## 3. No manual block for dependency waits

- [ ] 3.1 Refuse an agent's `block` action on a task with open dependencies and no other hold; owner's block allowed;
      refusal hint; tests.
- [ ] 3.2 Tool descriptions (`factory_task_block`, `factory_task_update`) and manager instructions: hold a task by a
      dependency, answer stalls with `keep_waiting`.

## 4. Verify

- [ ] 4.1 Replay the M5b-P3 case on a sandbox company: READY task waiting on two BACKLOG tasks; no escalation before
      the threshold, `keep_waiting` keeps the dependencies, the task starts when they are done.
