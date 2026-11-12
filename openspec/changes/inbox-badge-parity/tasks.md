## 1. Badge equals the Inbox

- [x] 1.1 `GET /alerts`: active task alerts and inspect parks with task, project, summary, since, clearing action;
      web BFF and TUI client; tests.
      Done: `storage.ActiveAlerts` lists every active `task_alerts` row of any kind (`comment_unhandled` included; a
      kind added later, e.g. by throughput-signals, is listed with no code change) and every active `inspect` park,
      with task title, project, assignee, ref, summary and since; Core's `GET /alerts` adds the clearing action per
      known kind and `with` (the assignee's lead for `no_progress`, `idle`, `stalled`, `inspect`). The TUI client and
      the web BFF overview read it (`Overview.alerts`). `PendingCounts` counts alerts and inspect parks from the same
      list and adds `owner_reviews`; `total` = Inbox messages + reviews for the owner + alerts. Tests:
      `TestAlertsListEqualsBadge`, `TestHTTPClientOverview`.
- [x] 1.2 Inbox "Alerts" section; alerts a lead can act on are listed too, marked "with <lead>"; badge and page total
      from the same items; a test that they match.
      Done: web `inbox/model.ts` builds the page's sections (decisions, reviews, reports, alerts) and the nav badge
      from the same overview items; the Inbox page shows an Alerts section (`AlertCard`: kind, "with <lead>", project,
      assignee, task link and title, summary, age, "To clear it"). TUI: the Inbox lists the Alerts below the messages
      (also when no message is open) and the header's ⚠ counts them. Core: `ownerView` in the tests asserts
      `pending.total == len(/inbox) + len(/alerts) + owner_reviews` in every scenario.

## 2. No false "no progress"

- [x] 2.1 `NoProgress` skips tasks that wait on someone (parked wait, owner or agent wait, pinned or queued wait);
      tests (parked producer task with "still waiting" runs raises no alert; a task with no wait still does).
      Done: `Store.NoProgress` skips a task with any active park or whose last ended run's exit was wait, ask or block
      (no flapping while the waiting task runs again); the stall sweep skips tasks whose verdict is parked,
      dependencies, capacity, a node wait (no capable / ready node, disk critical, project setup) or a pinned node.
      Test: `TestNoProgressSkipsRecordedWaits` (three "still waiting" runs over 9 h: no alert, also during the next
      run; that run failing without a wait raises it); `TestNoProgressAlert` still passes.

## 3. Promised details stay open

- [x] 3.1 Question forms: an "I'll send details" answer; such an answer keeps the question open as "details promised"
      and the asking task's reason says it waits for the owner's details; tests.
      Done: `POST /inbox/{id}/answer` takes `details: promised` (body: optional note); the question keeps its status
      with `details`, `details_note`, `details_at` (no migration: message bodies are JSON); its deadline no longer
      escalates it and auto-forward skips it. The web question card has "I'll send details"; TUI `p`.
- [x] 3.2 Inbox and Telegram show the open item; sending the details or withdrawing closes it.
      Done: the Inbox keeps it (card "Details promised …", "Send details" as free text whatever the form, "Withdraw
      promise"); Telegram lists it with "Details promised: reply with them here" and takes a free-text reply as the
      details. The details close it (`details: sent`): an answer to the question, or any owner message that wakes
      the waiting task; `details: withdrawn` answers "continue without them"; authority input that overrides the wait
      withdraws it. Tests: `TestPromisedDetailsReplayFE707C54`, `TestPromisedDetailsCloseWays`.
- [x] 3.3 The waiting task does not rerun on a timer while details are promised; it resumes when the details arrive
      or the promise is withdrawn (decision-waits mechanism if it has landed); tests.
      Done: a blocking question's task leaves its ask park for a `message` park on the owner (decision-waits'
      `parkOnMessageTx`, the transaction form of `Store.ParkOnMessage`, so the promise and the park commit together);
      its verdict is `parked:message`, who `owner`, "waiting for a message from owner about the details for question
      …". Test: 48 h without runs, then the details wake it with them in the brief.

## 4. Verify

- [x] 4.1 Replay `fe707c54`: the owner answers Q12 with "I'll send details"; the Inbox shows it open, no
      `no_progress` alert is raised, the badge equals the page; sending the details resumes the producer.
      Done as `TestPromisedDetailsReplayFE707C54` (fake node, httptest Core; the details sent from Telegram).
