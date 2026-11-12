Status: implemented 2026-11-12 except the prompt cleanup of 2.1; proposed 2026-11-12 by the owner. Blocker: the tvorog Launch Creative and Reviewer
prompts live only in the prod company's catalog, not in this repository; the owner drops their approval-comment line
there after this ships.

## Why

`owner-comments-handled` (implemented 2026-11-12) makes Core enforce an outcome for every board comment on a task.
The board also comments through approval decisions, and those are not covered.

Seen on 2026-11-11, tvorog task `2509ee3e` (Tracely screenshots): the board approved two screenshot samples with
comments:
- "норм, но я бы мб телефон больше сделал…";
- "гуд только первый скрин не надо выносить плажку".

The agent got each one as the reason of an `APPROVED` decision. Nothing required it to act on the comment or say what
it did. This time it did. But the owner could not see on the approval that it was applied, and thought it was not. The
only guard is a line in the Launch Creative and Reviewer prompts ("a comment on an approval is part of the decision,
even when approved").

## What Changes

- **A decision with a comment is a board comment.** When the owner decides an approval (artifact or command) with
  text beyond the default ("approved by owner"), Core records that text as a board comment on the approval's task,
  linked to the approval. The `owner-comments-handled` rules then apply as they are:
  - the next run's brief lists it;
  - every exit is refused until it has an outcome (done, declined with a reason, or forwarded);
  - the project lead gets the quiet copy.
- **Rejections too.** A rejection's reason is such a comment as well: the revision run must say what it changed.
- **Visible where the decision is.** The approval in the Inbox, on the task page and in the Library shows the comment's
  outcome ("done: frame 1 without the window", "declined: …", "not handled yet").
- **Prompts drop the workaround.** The Launch Creative and Reviewer lines about approval comments go once this ships.

## Impact

- Core: approval decisions with a reason create a comment linked to the approval (the comment_outcomes flow); approval
  reads carry the outcome.
- Web, TUI: the outcome under the approval (Inbox, task page, Library).
- Docs: `docs/artifact-approvals.md`, `docs/approvals.md`, `docs/task-lifecycle.md`. Related: `owner-comments-handled`.
