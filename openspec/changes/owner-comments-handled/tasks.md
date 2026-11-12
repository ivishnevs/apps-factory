## 1. Comment outcomes

- [x] 1.1 Exits carry one outcome per owner comment shown to the run (done, forwarded, declined with reason); every
      exit tool refuses while a shown owner comment has no outcome, listing them; a run ending without an exit leaves
      them open and a second such run raises an alert; tests.
      Migration 0037 `comment_outcomes` (one row per obligation: the commented task for its runs, a manager's or the
      lead's conversation task); every exit takes `comments: [{comment_id, outcome, reason?, task_id?}]` and answers
      409 `comments_unhandled` with the list; the brief lists the open ones with their ids; runs ending without an
      exit (attempt outcomes and lost, not stops) count `missed`; the stall sweep syncs alert `comment_unhandled`
      at two. Tests: `internal/core/comment_outcomes_test.go`.
- [x] 1.4 Core copies every owner comment on a project's task, with the assignee's outcome, into the project lead's
      conversation brief without waking it; it wakes the lead on a declined outcome, on no "done" past the team's limit
      (default one hour) and at once for locked categories; a forward to a manager who is the lead is one message; the
      lead's outcome is a linked task or a refusal; tests.
      Lead = the project team's lead (`Store.LeadOf`); limit = team `comment_outcome_within` (default 1h); locked
      category = `category` on the comment or `config.LockedCategoryOf` (fixed English/Russian word list).
- [x] 1.2 `factory_comment_forward {comment_id, note}`: verbatim forward up the chain to the assignee's manager with
      the task link, outcome "forwarded"; tests.
- [x] 1.3 Task page and TUI show each comment's outcome or "not handled yet".

## 2. Closed tasks

- [x] 2.1 Core refuses a comment on a DONE or CANCELLED task (409 naming reopen-with-comment and ask-the-lead);
      reopen with comment; tests. `POST /tasks/{id}/reopen`, `POST /tasks/{id}/ask-lead`; TUI actions `reopen`,
      `ask_lead`.
- [x] 2.2 Web task page: no comment box on a closed task; "Reopen with comment" and "Ask the lead about it".

## 3. Docs and verify

- [x] 3.1 Default agent instructions; `docs/task-lifecycle.md`, `docs/messaging.md`, `docs/owner-ui.md`.
      Exit instructions (`internal/core/instructions.go`), tool descriptions, the brief; also `docs/factory-mcp.md`.
- [x] 3.2 Replay 2026-11-11 with fakes:
      - "upload to TestFlight" on a developer's task is forwarded to the CEO and shows "forwarded to ceo";
      - a comment on the done task is refused with the two options.
      `TestBoardCommentReplay` (demo forwards to its manager, the team lead; "forwarded to manager by demo"),
      `TestBoardCommentQuietCopy`, `TestForwardUpTheChain`.
