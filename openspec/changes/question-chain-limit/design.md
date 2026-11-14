## Context

`sendMessageTx` counts `messages` rows with `message_type='question'` and a sender other than owner/factory in the
conversation or the task, and from the eighth sets the new question to `ESCALATED` (delivery `NONE`). `ReplyMessage`
refuses every non-owner answer to an `ESCALATED` question. A question sent through `factory_exit_ask` parks the task
on it.

## Decisions

### D1. What a chain is

A chain is the questions between two agents A and B (A→B and B→A) filed under the same task, or, for questions without
a task, in the same conversation, created after `max(now − 24 h, the owner's last message on that task)`. Both sides
must be agents (`waitsOnAgent`): questions to the owner, to members and from Factory never form a chain, so approval
requests and owner questions are not counted. The owner's message on the task (an answer, an approval decision) is
new outside input: the work has moved, so the count restarts.

A loop is fast: two agents answering each other in minutes. 8 questions between one pair in a day is far above the
review rounds seen in production (3–4 critique rounds a day between a designer and its reviewer), so the number stays 8
and the window is what changes.

### D2. Refuse, not escalate

The ninth question of a chain is refused before anything is stored: `*ExitRefusal{409, "question_limit", hint}`. In an
exit (`factory_exit_ask`) Core already answers refusals as 409 with the reason and hint; the message tools get the same
body (Core's `reply` learns `ExitRefusal`). The hint names the other agent, the count and the window, and the ways out:
decide within your authority and continue, put the rest into one message, or ask your manager (`factory_exit_ask`
routes up) or the owner.

Escalating instead stranded the task: the right answerer was locked out and the owner, who rarely is the right
answerer, became the only way forward.

### D3. Old escalations

A question whose `escalation_reason` is the old clarification limit may still be answered by its recipient. Deadline
escalations keep today's rule (only the owner answers once the deadline passed).

## Risks

- A real loop with the owner writing on the task between rounds is not stopped by this rule; the owner is then in the
  loop and sees it.
- An agent refused may switch to notifications; they do not park anything, so no task is stranded by it.
