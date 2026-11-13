## 1. Message wake routing

- [x] 1.1 `messageArrived`: a message filed under a task the recipient does not own also wakes the recipient's tasks
      parked on the sender; tests with fixtures (two agents, cross-task review request; message filed under the
      recipient's own task still wakes only that task; message filed under no task unchanged).

## 2. Wait gate

- [x] 2.1 Wait-for edges from active parks (message, awaiting_approval delegate, ask/escalation recipient) and the
      "stuck agent" check (no live or queued run, conversations included); unit tests over fixtures.
- [x] 2.2 `factory_exit_wait` from/approval_id: `wait_cycle` refusal (depth ≤ 4) naming the other task, what it waits
      for and its last message, with the fix; tests for a two-agent cycle, a three-agent cycle, and no refusal when the
      other agent has a live run.
- [x] 2.3 `wait_unasked` refusal and the `expects_unprompted` override recorded on the park; tests (verdict newer than
      the request → refused; request newer → accepted; override → accepted and shown).
- [x] 2.4 Exit instructions and the wait tool description mention both refusals and the override.

## 3. Approval holds

- [x] 3.1 A message filed under a held task from the approval's delegate (or an agent the delegate messaged about it)
      runs one turn; done and merge stay refused; test replaying the unpushed-commit case.
- [x] 3.2 A delegate waiting on, or asking, the requester while the approval is pending gets `wait_cycle` with the
      "decide changes requested" hint; test.

## 4. Sweep

- [x] 4.1 The stall sweep finds cycles over active parks and wakes each task once with a `wait_cycle` trigger; feed
      item `task.wait_cycle`; test.
- [x] 4.2 The same cycle within 24 h escalates once to the nearest common manager, else the project lead, else the
      owner, with `wake`, `reassign`, `cancel`; test.
- [ ] 4.3 Board and task view: a filter for tasks woken by `wait_cycle` (the park reason already shows "expects it unprompted").

## 5. Docs

- [x] 5.1 Explicit-exits contract: the two refusals, the override, held approvals hearing their delegate.
