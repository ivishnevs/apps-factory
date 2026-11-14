Status: accepted 2026-11-14 (owner: "fix the per-task communication limit") after two UI tasks of one project stalled on it within an hour.

## Why

Core bounds "automatic clarification chains" by counting every question an agent ever sent in a task (or its
conversation) and escalating the eighth to the owner (`sendMessageTx`, `internal/storage/messages.go`). The count has no
time window, no notion of who asks whom, and includes questions to people: an approval request and a question to the
owner count the same as a question to another agent.

On 2026-11-14 a production company hit it twice in an hour:

1. A designer's hub task had nine questions over 13 hours, all answered: three critique rounds with the game designer,
   one production check by the producer, an approval request and two questions to the owner, across two owner reviews.
   The ninth, the designer's request for the game designer's critique, was escalated to the owner. A question escalated
   this way cannot be answered by its recipient (the game designer was refused), only by the owner, and the asking task
   is parked on it, so the task stopped until the owner, who was not the right person to answer, answered.
2. The same happened an hour later on a talent-tree task of the same designer.

The guard exists against agents asking each other in a loop. Long tasks with review rounds, and questions to people,
are not loops, and the escalation strands the task instead of stopping the loop.

## What Changes

- **A chain is a pair of agents on one task in a recent window.** The count covers questions between the same two
  agents (either direction) about the same task (or, without a task, in the same conversation), within the last 24
  hours and since the owner last wrote on that task. Questions to or from people (owner, members), Factory's own
  questions and approval requests are not counted.
- **The limit refuses instead of escalating.** The ninth question of a chain is refused with `question_limit`: nothing
  is sent, the run keeps going, and the refusal names the other agent, the count and what to do instead (decide within
  your authority, send the rest as one message, or ask your manager or the owner). The owner is never refused.
- **Questions already escalated by the old limit stay answerable** by their recipient, so tasks parked on them today
  are released by the agent that should answer.

Out of scope: the answer deadline escalation (unchanged), duplicate-question rejection (unchanged).
