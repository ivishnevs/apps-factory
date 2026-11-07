Status: bug recorded 2026-11-05 by the owner; fixed 2026-11-07 (see "Implementation").

## Why

Everything a person does is recorded as "owner". In a company with several people (the owner and an admin
partner on the game project) the activity feed, task history and chats show every action as done by the owner:
a task the admin created shows "created by" the owner, and in the chat with the team lead nobody (people or the
agent) can tell who wrote which message. This is a bug: Core knows the signed-in person of every web and
Telegram request, but tasks, messages, answers, reviews, approvals and actions store only `owner`
(`messaging.Send{Sender: "owner"}`, `created_by: "owner"`).

## What Changes (proposed)

- Record the person on everything people do: task creation and actions (run, pause, cancel, reassign, directives,
  comments, reviews), messages and answers, approvals and decisions, catalog changes, invitations, settings. The
  operator and owner tokens (CLI) record themselves as such.
- Show the person everywhere: activity feed, task details and history ("created by Insolo647"), chat bubbles with
  the sender's name and avatar, inbox answers ("answered by …"), catalog history.
- Agents see who wrote: a message from a person carries their name and role in the agent's brief, so the lead
  can tell the owner and a manager apart and address them.
- Telegram messages are attributed to the person bound to that chat.
- Keep `owner` as the role that agents report to (hierarchy unchanged); the person is an additional field.
- Existing rows stay "owner" (unknown person); no backfill.

## Impact

Storage (person columns on tasks, task events, messages, approvals, catalog versions), Core routes taking the
session's person, web feed, task, chat and inbox views, agent briefs, Telegram. Related: a human manager per
team (questions of a team go to its manager) may follow as a separate change.

## Implementation (2026-11-07)

- Design (decided during implementation): Core puts the person into the request context — the signed-in member
  (session) or the person bound to a Telegram chat; storage records it next to "owner" wherever an owner action is
  written: messages (`person`, `person_id`), tasks (`created_by_person`), comments and feed rows (`person` columns,
  migration 0022), approvals (`decided_by_person`) and decisions (`recorded_by_person`). The CLI's tokens and the
  operator carry no person. Existing rows are not backfilled. A name is the member's name, else the e-mail's local
  part.
- Agents: a person's message in a brief carries "Written by NAME (a person of the company; owner is the role you
  report to)".
- Web: task "Created … by NAME", comments, chat bubbles of other people and feed rows show the person; the signed-in
  person still sees "you".
