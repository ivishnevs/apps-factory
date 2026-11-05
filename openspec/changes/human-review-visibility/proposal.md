Status: idea recorded 2026-11-05 by the owner; not designed or implemented.

## Why

On the game project a person assigned a simple check to the team lead (`producer`). The lead is a manager, so no
review rule covers it; it submitted the task for review with no agent reviewer, and the task sat in "In review"
with nobody visibly responsible. Nothing told the people that they would have to review it, nothing happened
when it reached review, and the task did not show whom it was waiting for. (Accept / Send back buttons were
added on 2026-11-05; this change is about knowing that and when a person must act.)

## What Changes (idea, to be designed)

- **Warn up front.** When a person assigns a task to an agent whose work has no agent reviewer (for example the
  team lead), say so when creating or reassigning it: "producer's work on this task comes back to you for
  review".
- **The reviewer is never empty.** A task in review that waits for people has its reviewer set explicitly to
  "Owner" (or the person who must review), shown on the board card, the task and the review column, exactly like
  an agent reviewer; an empty reviewer is a bug (owner, 2026-11-05: "otherwise nobody answers and the task
  hangs; this is a blocker").
- **Say who is awaited when the task reaches review.** A task in review without an agent reviewer names the
  person (or people) it waits for, shown on the task, on the board card and in the activity feed, for example
  "Waiting for your review (Ilya)".
- **Notify that person.** An Inbox item and a Telegram message ("producer finished 'precheck': accept or send
  back"), with the Accept / Send back actions in both.
- **Who that person is** (to decide): the task's creator; or the team's human manager when one is set; or the
  company's owners as the fallback. Needs people attribution (`people-attribution`) to know the creator.
- Possibly: the lead finishes tasks without code or review rules as done instead of submitting them (prompt or
  Core rule), so trivial tasks do not wait for a person at all.

## Impact

Task creation and reassign dialogs, task view and board, activity feed, inbox, Telegram notifier, review
resolution in Core. Related: `people-attribution`, `team-page-clarity`.
