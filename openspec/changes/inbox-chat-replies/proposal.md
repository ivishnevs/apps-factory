Status: problem recorded 2026-11-05 by the owner; to be designed. Nothing implemented.

## Why

The owner answered the producer's escalation with questions; the producer's reply (with its own questions: "Ok?",
"choose a map rotation: a / b / c") arrived in the Inbox under "To read", where the only action is "Mark as read".
There is no way to answer it there, and the "in chat" link opens the generic Chat page, not that conversation.

How it works today (checked 2026-11-05):

- That reply was not in the Chat at all: it was a `notification` in the escalation's own conversation
  (`chain-<hash>`), not in the owner's chat with the lead (`owner-<agent>-<project>`). The Chat page shows only
  the latter, so opening the chat could not help, and a notification cannot be answered anywhere. an agent's reply in a conversation is an `answer` message; the Inbox lists
it as "To read" (read-only). Only `question` messages (`factory_owner_ask`) and approvals get an answer box or
buttons. Agents often ask follow-up questions inside an `answer`, so people get questions they cannot answer from
where they see them.

## What Changes (proposed)

- **Reply from the Inbox.** Every Inbox item that belongs to a conversation gets a Reply box (sent to that
  conversation, like writing in the chat) next to "Mark as read"; replying marks it read.
- **Escalation threads are visible and answerable.** Messages of an escalation chain (`chain-…`) show in the Chat
  (as their own thread or inside the chat with that agent), and the person can reply there.
- **Open the right chat.** "in chat" opens that exact conversation (project and agent), scrolled to the message.
- **Options are choosable.** A notification or answer that lists options ("A, B or C?") shows them as buttons
  (and a free-text reply) instead of only "Mark as read" (see `decision-approval-ux` for decisions).
- **Questions are questions.** When an agent's message asks the person to choose or confirm, it is sent as a
  question (with options when they are listed) so it shows under "Needs your answer" with buttons; lead
  instructions and the tool make this the default for messages to people that end with a question.
- Same in Telegram (`telegram-reply-routing`).

## Impact

Web Inbox cards and chat deep links, agent messaging instructions (ask vs answer), possibly Core detecting
questions in answers. Related: `decision-approval-ux`, `telegram-reply-routing`, `lead-brainstorm-mode`.
