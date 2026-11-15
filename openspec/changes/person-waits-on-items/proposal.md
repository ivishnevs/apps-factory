Status: accepted 2026-11-15. Discussed with the owner the same day from the known issue "A task waits for 'a message
from the owner' when it waits for a decision" (`docs/backlog.md`, recorded 2026-11-13); the owner chose the full fix
("do it right") over a display-only hint.

## Why

On 2026-11-13 in tvorog the product manager's task 553cc263 (stashy prototype v4) depended on the CEO's decision
proposal `ac3f2bc2`, which waited in the owner's Inbox. The product manager exited `factory_exit_wait` with
`from: owner` instead of `decision_id`. The board showed "Waits for a message from you · Write to its agent", sending
the owner to write to the agent instead of deciding the proposal. Three things were wrong:

1. **The board pointed to the wrong action.** A wait "for a message from the owner" has nothing in the Inbox to
   answer, so the board can only say "write to its agent".
2. **The task could wake on an unrelated message** (any owner message to the agent), see the decision still open,
   and park again: a wasted run.
3. **Deciding the proposal did not wake it.** A recorded decision wakes only tasks parked on that decision; this one
   waited on a message, so it could sleep past the decision.

The decision was proposed from the CEO's conversation (`task_id` `conv-ceo-stashy`), which is the product manager's
task's `origin_task`: the link exists in the data, nothing used it.

The class: a wait on a person with no item the person can answer. It is the same failure as the 2026-11-15 forward
(`forward-refusal-guard`): the board says "needs you" while the Inbox has nothing.

## What Changes

- **A task waits for a person only through an item in their Inbox.** `factory_exit_wait` with `from: owner` is
  accepted only when the owner has an open item for the task: a decision, a question or an approval (below). With none,
  it is refused with `wait_unanswerable` and the hint: ask with `factory_exit_ask` (the question holds the task and its
  answer wakes it) or propose a decision and wait on it with `decision_id`. Waits on an agent (`from: <agent>`) are
  unchanged (`wait-deadlocks` checks them). The owner's message to the agent still wakes the task (authority input).
- **An open item is linked by Core.** For a task T, the owner's open items are:
  - open decisions of the project proposed from T, T's parent or T's origin task (a lead proposing for the task it
    delegated, as the CEO did here);
  - open approvals holding T (approval-gates-merge);
  - open questions to the owner about T.

  With open decisions, Core parks T on them (kind `decision`; the board then says "Waits for your decision: <topic>"
  and links to it) and the exit result names them. With an approval holding T, Core parks T on it as
  `awaiting_approval`. With only a question, T keeps its message park on the owner: the owner's answer is a message
  about T and wakes it.
- **A recorded decision wakes the tasks waiting on the owner for it.** When a decision is recorded (approved,
  rejected, withdrawn), besides the tasks parked on it, the open tasks of the project that are proposed from or
  delegated from its task (T itself, or tasks whose parent or origin is the decision's task) and are parked on a
  message from the owner or on another decision of the same task get the `decision_recorded` trigger with the
  outcome and their parks clear. Tasks not waiting are not disturbed; closed tasks are skipped.
- **No migration.** Tasks already parked on `from: owner` keep their park (they still wake on the owner's message,
  and now also on a related decision); the rule applies to new waits. The project is unreleased, so no compatibility
  path is kept for the old exit.

Out of scope: a chat message written past an open item's card stays a directive to the agent (owner's choice,
2026-11-15: no "is this an answer to X?" matching); waits between agents (`wait-deadlocks`).

## What was discussed

- Least invasive options were a board hint only, or a hint plus "a decision wakes its task". The owner asked for the
  right fix: every wait on a person is a wait on an item they can answer, so the board always points to that item and
  its answer always wakes the task.
- Refusing `from: owner` with a hint (like the existing `wait_on_decision` for timed waits) costs a model turn and
  relies on the model; linking an item Core already knows is deterministic, so Core links when it can and refuses only
  when there is nothing to link.
- Answering stays possible from the chat: questions and decisions come as cards in the chat with the project's lead
  (`inbox-chat-replies`, `agent-questions-in-chat`), and answering the card answers the item.
