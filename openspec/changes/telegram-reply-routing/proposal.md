Status: problem recorded 2026-11-05 by the owner; to be verified and designed. Nothing implemented.

## Why

The owner and an admin both work with the game team and both use the company's Telegram bot. Messages written in
Telegram go into the web chat, but the agent's answer stays in the web and does not come back to the Telegram
chat it was asked from; the reverse (a web message answered only in Telegram, or one person's Telegram getting
the other's answers) may happen too. With two people the result is confusing: you ask in Telegram and must open
the web to see the answer, and you cannot tell whose conversation an answer belongs to.

What the code does today (checked 2026-11-05):

- A Telegram message from any person goes to the one shared conversation of the agent and project
  (`owner-<agent>-<project>`), the same one the web chat shows, with sender `owner` for everyone.
- Telegram only receives "attention" feed items (`telegramBackend.Attention`): agents' questions (owners only) and
  alerts (owners and admins), plus reminders and the digest. Ordinary agent replies in the conversation are not
  sent to Telegram at all, so an answer to a Telegram message is seen only in the web.
- An admin never gets agents' questions in Telegram (`OwnersOnly`), even when the admin asked the question that
  led to it.

## What Changes (to be verified and designed)

- **Answer where you were asked.** An agent's reply to a message that came from a person's Telegram chat is sent to
  that person's Telegram chat (and stays in the web conversation), as a reply to the original message where
  possible.
- **Per person, not per company.** Replies and follow-up questions go to the person who wrote, not to everyone and
  not only to owners; other people see them in the web (and in the digest) without being pinged.
- **Shared conversation, visible senders.** Keep one conversation per agent and project, with each message showing
  who wrote it (`people-attribution`); decide whether people should also have private threads with a lead.
- **Verify all directions**: Telegram → agent → Telegram; web → agent → web only; person A's Telegram vs
  person B's; questions to admins who started the topic; replies routed by reply, `@name` and "last message" as
  today.
- Tests for two people with two Telegram chats against one lead.

## Impact

Telegram notifier and backend (outbound of conversation replies, per-person routing), messaging (origin of a
message: web or the person's Telegram chat), web chat. Depends on `people-attribution`; related:
`human-review-visibility`, `lead-brainstorm-mode`.
