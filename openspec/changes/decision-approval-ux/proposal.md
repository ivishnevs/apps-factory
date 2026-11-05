Status: owner chose A and C on 2026-11-05 (actionable proposals in the Inbox, plus approving from the chat with an
explicit action); to be designed. Nothing implemented.

## Why

On the game project the producer proposed a decision and waited for it in the chat, while the owner only got an
Inbox notification with no way to approve it. The producer explained afterwards: `factory_decision_propose` only
puts the decision in the registry as `proposed` and notifies the owner; to make it approved the agent must send a
separate approval request for the exact call "record this decision as approved" (`factory_decision_record`), which
it sent only after the owner asked why there was nothing to approve. The owner could not approve it by writing in
the chat either.

How it works today (checked 2026-11-05):

- Proposals and approved decisions are separate on purpose; the `reports-decisions` spec says "chat and task review
  SHALL NOT approve them", so an "ok, approved" in the chat does not approve anything.
- An agent records an approved decision only with an approval of that exact call; that approval appears in the
  Inbox under "Needs your decision" with Approve / Reject.
- Core already has an owner route to record a proposed decision directly (`POST /decisions/{id}/record`), but the
  web does not offer it: a proposal shows as a notification without actions.
- So the owner sees a proposal they cannot act on, the agent waits, and the flow depends on the agent remembering a
  second step.

Second case (2026-11-05, character class names): the lead sent the options (a recommended name and alternatives) as
an Inbox notification the owner could only mark as read, nothing appeared in the chat, and then an approval arrived
with only the recommended option, so choosing another option or discussing nuances meant "Reject" with a comment.
Choices with alternatives must be a conversation that ends in a decision, not a yes/no on the agent's pick.

## Options to weigh

- **A. Proposals are actionable.** A proposed decision appears in the Inbox under "Needs your decision" with
  Approve, Reject (with a reason) and Comment; Approve records it through the owner route. No second request from
  the agent. Simplest for people.
- **B. One step for the agent.** `factory_decision_propose` itself creates the approval request when the decision
  needs the owner, so the agent cannot forget it; the Inbox item is that approval.
- **C. Approve from the chat.** An explicit "approve" on the proposal's message in the chat (a button on the message,
  or a reply to it in the web or Telegram) approves it, and the Inbox item disappears. Free text such as "ok, go"
  is not enough on its own: the agent may ask "record it as approved?" with buttons. Keeps the rule that a chat
  message alone does not authorize, while removing the extra hop.
- **Choices, not yes/no.** A proposal with alternatives shows all of them (with the recommendation marked) in the
  Inbox and in the chat; the person picks one, or writes their own, and that choice is what gets recorded, not only
  the agent's recommendation.
- **Discuss before deciding.** Besides Approve / Reject there is "Discuss": the item stays pending (unread in the
  Inbox) and the conversation continues in the chat with the lead (brainstorm: "what about X?"); when the person
  approves in the chat (explicit action), the decision is recorded and the Inbox item disappears.
- **Always in the chat.** Proposals and their discussion live in the person's chat with the lead (not only in an
  escalation thread or an Inbox notification); the Inbox is the to-do list that points to it.
- Whatever is chosen: one pending item per decision (approving in one place clears it everywhere), the agent is
  told the outcome at once, and the Telegram notification carries the same Approve / Reject.

## Decision (2026-11-05)

A and C: proposals are actionable in the Inbox (Approve, Reject with a reason, Comment), and a proposal can be
approved from the chat with an explicit action on its message (a button, or a reply the agent confirms with buttons);
deciding in one place clears it everywhere. B (propose creates the approval) may still be used inside Core to
implement A.

Also found on 2026-11-05: an approval whose question was withdrawn (a newer owner message superseded the ended
run's question) could not be decided at all ("decided, expired or changed meanwhile"); fixed in Core the same day.

## Open questions

- Whether some decision categories need no owner at all (decision-autonomy already
  delegates categories such as technical).
- How a rejection with a reason flows back to the agent (decision-autonomy's durable rejection follow-up).

## Impact

Decision tools (propose may create the approval), Inbox and chat UI (actions on proposals), Telegram buttons,
`reports-decisions` spec wording. Related: `human-review-visibility`, `lead-brainstorm-mode`,
`telegram-reply-routing`.
