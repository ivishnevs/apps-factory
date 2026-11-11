Status: proposed 2026-11-11 by the owner ("why did the approval come to the Inbox and not to the chat with the
manager; it would be great to have it in the task too"). Nothing implemented.

## Why

Seen on 2026-11-11 in tvorog. App Release asked the owner on task `44b64e7d` ("Tracely → TestFlight"), category
`release`, blocking: "Which App Store Connect account (asc profile) should I use for the Tracely TestFlight upload?
Profiles: ASC Key, Cheflet, Cheflet-H7N3F3AN5P (default), Stashy." The owner was talking to the CEO in the tracely chat
and did not see the question there:

- **The chat shows only direct reports.** The web Chat lists the owner's conversations with direct reports
  (`owner-<agent>-<project>`, the CEO here). App Release reports to the CEO, so its question went to an escalation
  thread (`chain-…`), which no chat shows. `inbox-chat-replies` (2026-11-07) moved questions from leads into the chat,
  but questions from other agents of the team stayed in the Inbox only.
- **The task page sends the owner away.** The task's "needs you" block has only "Open in inbox".
- **Listed options were plain text.** The four profiles were in the body, not as options, so the owner had to type
  "Stashy" by hand.

## What Changes

- **Questions from any agent of a project come in the chat with the project's lead.** An open question or approval
  from an agent that is not the owner's direct report shows in the owner's chat with that project's lead as a card:
  who asks ("App Release asks"), the task, the question, its options or answer box. Answering there answers the
  question itself (same message, same compare-and-set). The lead's conversation brief shows the question and the
  answer, so the lead knows what was decided.
- **The task page answers in place.** The open question or approval of a task shows on the task page with its options
  and answer box. "Open in inbox" stays as a link.
- **Listed choices are options.** `factory_owner_ask` (and the escalation tool) accepts and expects `options` when the
  agent offers choices. Agent instructions ask for options, and the tool's result hints when a question lists choices
  ("A, B or C", a bullet or comma list after "Profiles:") without options. Each option becomes a button with "Other"
  for free text.
- The Inbox stays the list of what is open; an answer from the chat, the task page, the Inbox or Telegram closes it
  everywhere.

## Impact

- Web: chat thread merging (lead chat plus open questions of the project's other agents), task page answer block,
  option buttons.
- Core: list a project's open owner questions and approvals with their asking agent for the chat; include them in the
  lead's conversation brief.
- Messaging tools and agent instructions: options for listed choices.
- Docs: `docs/messaging.md`, `docs/owner-ui.md`. Related: `inbox-chat-replies` (done), `decision-approval-ux`,
  `telegram-reply-routing`.

## Note for the tvorog catalog (not part of this change)

The PM's prompt already says TestFlight uploads use the Stashy profile, but App Release's prompt does not, so it asked.
Adding that rule to App Release saves the question.
