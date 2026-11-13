Status: proposed 2026-11-13 after the owner found three tasks of one project asleep on each other.

## Why

Agents park on each other with `factory_exit_wait` (`from`, `approval_id`) and nothing checks whether the thing they
wait for can still happen. On 2026-11-13 one project of a production company had three dead waits at once, each found
only because the owner asked why work had stalled:

1. **Lost wake between two tasks.** ui-designer's task B waited for "the next message from ui-dev". ui-dev's task F
   waited for ui-designer's re-review. ui-dev did send the re-review request (21:21 UTC), but `factory_message_send`
   files a message under the sender's own task unless `task_id` is set, and `messageArrived`
   (`internal/storage/eventwaits.go`) wakes only the task a message is filed under, or the recipient's tasks when it is
   filed under no task. A message about the sender's task F, addressed to agent ui-designer, woke nobody, although
   the park's own text promises "the next message from ui-dev to B or its agent wakes it". Both tasks slept until their
   deadlines, a week away.
2. **Wait without asking.** ui-dev's task P pushed a fix after ui-designer's "changes requested" and parked on a message
   from ui-designer without telling ui-designer there was a new head. ui-designer had nothing pending, so no message
   would ever come.
3. **Approval deadlock.** artist-2d requested approval of an artifact whose commit was not on origin. The approval was
   delegated to producer, who asked game-designer to review first and kept the approval pending. game-designer could
   not review the unpushed commit and asked artist-2d to push. artist-2d's task was parked as `awaiting_approval`, which
   by design only the decision wakes (no message, not even a directive). Each of the three waited for another. The
   approval would have expired after 24 h and then sent the task elsewhere.

None of these is specific to that company's setup. Any company with reviews across tasks, delegated approvals or two
agents that hand work back and forth can hit them, and they look the same from the outside: tasks READY with a
reasonable-sounding `parked:*` reason that never changes.

## What Changes

- **Message wakes reach the waiting agent.** A message from S to agent R that is filed under a task R does not own also
  wakes R's tasks parked on a message from S, as a message filed under no task does today. A message filed under R's
  own task keeps waking only that task.
- **A gate on the wait exit (the main change).** When an agent ends a run with `factory_exit_wait` on another agent X
  (a message from X; an approval X decides), Core checks the wait against the open waits of the project and refuses two
  shapes with a refusal the agent can act on:
  - `wait_cycle`: X is itself parked waiting on this agent (a message from it, an approval it holds, a question to
    it), and neither has a live run. The refusal names X's task, what X waits for and the last message X sent, and
    says what breaks the cycle: answer X first (with X's task id), or, for a held approval, decide it as changes
    requested.
  - `wait_unasked`: X has nothing pending from this agent: X's last message to the agent is newer than the agent's last
    message to X, or there is none. The refusal says that X does not know it is awaited and to send the request first.
    When X has promised to write (its last message says it will), the agent exits again with `expects_unprompted: true`
    and the wait is accepted.
- **Holds still hear their delegate.** A task parked as `awaiting_approval` wakes for one turn on a message from the
  approval's delegate or from an agent the delegate asked to review (messages filed under the held task). The hold
  stays: done and merge are refused as today, and the turn ends by waiting on the approval again. A delegate that tries
  to wait on the requester while the approval is pending gets `wait_cycle` with the hint to decide changes requested.
- **A sweep for cycles formed later.** Waits that become a cycle after both exits (one agent's run ends after the other
  parked) are caught by a periodic check of the same rule over active parks. Each task in the cycle is woken once with
  a `wait_cycle` trigger that names the other side and its last message. A cycle that forms again within a day escalates
  to the agents' nearest common manager (or the project lead) instead of waking them again.

The rule works on agents and parks only, never on roles, team shapes or names. Waits on people (owner, members),
timers, decisions and dependencies are not part of it.

Out of scope: why the delegate kept the approval pending (prompt guidance, done by hand for the affected company), and
reports that need an owner action reaching the Inbox (a separate change).

## Owner decisions

1. `wait_unasked` as a refusal with an explicit override (`expects_unprompted`), or only as a warning in the exit
   answer? Recommended: refusal with override; warnings at exit are read after the run has ended.
2. The sweep wakes both sides of a cycle once, then escalates on repeat. Accept, or escalate on the first cycle?
3. Waking `awaiting_approval` on messages from the delegate's side (one turn, hold kept) — accept?
