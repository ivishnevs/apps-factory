Status: implemented 2026-11-12. Proposed 2026-11-07 by the owner ("write down the idea: a task waiting on a decision
should not wake on a timer"). Widened 2026-11-12 (throughput-signals, owner): a task waits on an event, a decision
being one kind; the same shape covers a message from a named agent or the owner and promised owner details
(inbox-badge-parity, which calls `Store.ParkOnMessage`; wiring it is that change's task).

Implementation decisions not settled above: the tool is `factory_exit_wait` (there is no `factory_task_park`); a
withdrawn decision is a superseded one (decisions have no other withdrawal); a timed wait naming an open decision is
refused (`wait_on_decision`), not only hinted; authority input (directive, authority comment) overrides an event park
like a timed wait; a message about a task wakes only that task, a message about no task wakes the recipient's tasks
parked on that sender; the deadline escalation (`event_wait_deadline`: keep_waiting, continue, cancel) goes to the
owner for a decision or an owner message, else to the assignee's manager; dependents show the root up to three
levels down.

## Why

Seen on the game project, task VP-2 `575b3ebf` (PR #182), 2026-11-07:

- game-designer proposed decision `e4a63d3d` (Filmic instead of AgX) from VP-1 `f3feb881`, which is DONE. The proposal
  reached the owner as a non-blocking `review_request` tied to VP-1.
- VP-2 needs that decision to merge. It parked with a plain timed `wait` ("Owner decision e4a63d3d is still
  proposed"), 12 waits until 2026-11-12. Every wake rebases the branch, reruns the node checks and parks again:
  runs 7 and 8 cost $3.26 and $3.22 and changed nothing; about ten more wakes were scheduled.
- Nothing linked VP-2 to the decision: the board showed VP-2 in Blocked as "wakes at its time", "5 need you" did not
  count it, and the owner did not find the decision as the blocker of VP-2 (it sat among reports, attached to a
  finished task). VP-4, VP-5 and the polish plan waited behind it.

## What Changes

- **Wait on a decision.** `factory_task_park` (and the wait tools) accept `decision_id`: the task parks with kind
  `decision`, without a timer. It wakes when the decision is recorded (approved, rejected or changed) or withdrawn,
  with the outcome in its brief. A deadline stays optional and only escalates to the decision's addressee, it does not
  wake the agent to poll.
- **The decision knows who waits.** Recording a decision lists and wakes every task parked on it; the decision card
  (Inbox, chat, task page) shows "VP-2 and 3 more wait on this".
- **Board and counts.** A task parked on a decision shows "Waits for your decision: <topic>" with a link to decide,
  counts in "need you" for the decision's addressee, and its dependents show the same root.
- **Agents use it.** Tool descriptions and the default manager and developer instructions: when work needs a pending
  decision, park on the decision instead of a timed wait; a timed wait that names a proposed decision gets a hint to
  use `decision_id`.

- **Wait on a message or promised details (same mechanism).** The park tool also accepts `from` (an agent or the
  owner, optionally `topic`): the task parks with kind `message` and wakes on the next message from that sender to the
  task or its agent in the project, with the message in its brief. Seen 2026-11-08: six tasks parked waiting for the
  producer's "your turn" and woke every 1–2 hours to find nothing. A question answered with "I'll send details"
  (inbox-badge-parity) parks the asking task the same way on the owner's details.

Not in scope: deciding automatically on timeout.

## Impact

Park kinds and wake triggers (`internal/storage/parks.go`, decision recording), Factory MCP park tool schema and
descriptions, board and Inbox (web, TUI), "need you" counts, agent instructions. Spec: `task-engine`. Related:
`decision-approval-ux` (proposals in the Inbox), `park-watchdog`.
