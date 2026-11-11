## 1. Comment outcomes

- [ ] 1.1 Exits carry one outcome per owner comment shown to the run (done, forwarded, declined with reason); Core
      keeps unhandled comments as open triggers and raises an alert when a second run skips one; tests.
- [ ] 1.2 `factory_comment_forward {comment_id, to, note}`: verbatim forward to the manager or project lead with the
      task link, outcome "forwarded"; tests.
- [ ] 1.3 Task page and TUI show each comment's outcome or "not handled yet".

## 2. Closed tasks

- [ ] 2.1 Core refuses a comment on a DONE or CANCELLED task (409 naming reopen-with-comment and ask-the-lead);
      reopen with comment; tests.
- [ ] 2.2 Web task page: no comment box on a closed task; "Reopen with comment" and "Ask the lead about it".

## 3. Docs and verify

- [ ] 3.1 Default agent instructions; `docs/task-lifecycle.md`, `docs/messaging.md`, `docs/owner-ui.md`.
- [ ] 3.2 Replay 2026-11-11 with fakes:
      - "upload to TestFlight" on a developer's task is forwarded to the CEO and shows "forwarded to ceo";
      - a comment on the done task is refused with the two options.
