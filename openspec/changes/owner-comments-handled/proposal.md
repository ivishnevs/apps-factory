Status: proposed 2026-11-12 by the owner ("I left a comment to upload to TestFlight and it was ignored; comments on a
done task are ignored too, the UI should just hide sending a comment there"). Nothing implemented.

## Why

Seen on 2026-11-11 in tvorog, task `a8ad9e45` (Tracely: a sheet scroll moves the elevation scrubber), developer-lite:

- **An out-of-role comment was dropped.** At 22:55 the owner commented "then upload the fix to TestFlight right away".
  Developer-lite read it ("TestFlight belongs to App Release and starts only after the board's word and the CEO's
  task") and did nothing with it: no forward, nothing in its summary. The CEO then told the owner "waiting for your word
  on TestFlight", though the word had been given. Nothing in Factory shows whether a comment was acted on.
- **A comment on a done task went nowhere.** At 23:16, after the task was DONE, the owner asked on it "why didn't you
  check on the simulator after the second review?". A closed task never runs again, so no one read it, yet the web
  offered the comment box as on an open task.

## What Changes

- **Each owner comment shows what became of it.** The run that gets an owner comment records, in its exit, one outcome
  per comment: done, forwarded (to whom, with the message), or declined (with a reason the owner sees). The task page
  and the TUI show the outcome under the comment, and "not handled yet" while none is recorded. A run that ends without
  an outcome for a comment it was shown keeps the comment as an open trigger and raises an alert after the next run
  that skips it.
- **Out-of-role comments are forwarded, not dropped.** `factory_comment_forward {comment_id, to, note}` sends the
  comment verbatim, with the task link, to the agent's manager or the project lead, and records the outcome
  "forwarded". The agent instructions name it. (The tvorog catalog got the matching prompt rule on 2026-11-12.)
- **No comment box on a closed task.** A DONE or CANCELLED task's page offers instead:
  - "Reopen with comment": the task goes back to its assignee with the comment;
  - "Ask the lead about it": a message to the project lead with the task linked.
  Core refuses a plain comment on a closed task (409 with these two options), so the TUI, CLI and Telegram behave the
  same.

## Impact

- Core: comment outcomes on exits, the `factory_comment_forward` tool, the unhandled-comment alert, refusing comments
  on closed tasks, reopen with comment.
- Web, TUI: comment outcomes on the task page, closed-task actions.
- Agent instructions (Factory's default library) for handling comments.
- Docs: `docs/task-lifecycle.md`, `docs/messaging.md`, `docs/owner-ui.md`. Related: `agent-questions-in-chat`.
