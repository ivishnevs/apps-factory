## 1. Badge equals the Inbox

- [ ] 1.1 `GET /alerts`: active task alerts and inspect parks with task, project, summary, since, clearing action;
      web BFF and TUI client; tests.
- [ ] 1.2 Inbox "Alerts" section; badge and page total from the same items; a test that they match.

## 2. No false "no progress"

- [ ] 2.1 `NoProgress` skips tasks that wait on someone (parked wait, owner or agent wait, pinned or queued wait);
      tests (parked producer task with "still waiting" runs raises no alert; a task with no wait still does).

## 3. Promised details stay open

- [ ] 3.1 Question forms: an "I'll send details" answer; such an answer keeps the question open as "details promised"
      and the asking task's reason says it waits for the owner's details; tests.
- [ ] 3.2 Inbox and Telegram show the open item; sending the details or withdrawing closes it.

## 4. Verify

- [ ] 4.1 Replay `fe707c54`: the owner answers Q12 with "I'll send details"; the Inbox shows it open, no
      `no_progress` alert is raised, the badge equals the page; sending the details resumes the producer.
