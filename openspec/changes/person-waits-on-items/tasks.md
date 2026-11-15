## 1. Waits on the owner go through an item

- [x] 1.1 Core: the owner's open items for a task (open decisions proposed from the task, its parent or its origin;
      approvals holding it; open questions to the owner about it).
- [x] 1.2 `factory_exit_wait from: owner`: refused with `wait_unanswerable` and the hint when there is no item; with
      decisions, parked on them (kind decision) and the exit result names them; with an approval, parked as
      awaiting_approval; with only a question, the message park stays.
- [x] 1.3 Tests: the 2026-11-13 case with fixtures (a lead's decision from its conversation, the delegated task waits
      `from: owner` → parked on the decision, board reason "waits for your decision"); no item → refused; question
      only → message park; approval → awaiting_approval; a wait on an agent unchanged.

## 2. A recorded decision wakes its waiting tasks

- [x] 2.1 `decisionRecorded`: the open tasks of the project from the decision's task (the task itself, tasks whose
      parent or origin it is) parked on a message from the owner or on another decision of that task get the trigger
      and their parks clear; others are not disturbed.
- [x] 2.2 Tests: decided → the waiting delegated task wakes with the outcome; a task of the same origin that is not
      waiting is untouched; a closed task is skipped.

## 3. Docs

- [x] 3.1 Explicit-exits contract (wait) and the backlog: the known issue is closed by this change.
