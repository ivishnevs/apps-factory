Status: proposed 2026-11-09 by the owner ("the inbox shows 1, but it is empty when I open it; this did not happen
before"). Open questions decided by the owner 2026-11-12 (below); ready to implement. Nothing implemented.

## Why

Seen on 2026-11-08 in ilya-75ba24: the Inbox badge showed 1 and the Inbox page said "You're all caught up".

- **The badge and the page count different things.** The badge is `pending.total` = open inbox items + active task
  alerts + runs to inspect (`storage.PendingCounts`, since 2026-10-13). The Inbox page lists only messages, approvals
  and reviews; alerts and inspect parks are not listed anywhere, and there is no API to list them, only the count. Since
  the Inbox lists only what is open (decision-approval-ux, 2026-11-07), an empty page with a lit badge is common.
- **The alert was a wait on the owner, shown as "no progress".** The one alert was `no_progress` on the producer's
  task `fe707c54` (Spec: M7 iOS TestFlight). Its runs on 11-06, 11-07 and 11-07 late all ended with "still waiting
  for your answer on the Apple team (Q12), which you said you'd send separately": the owner had answered the question
  with "Other — I'll write details" and the details never came. The question was closed by that answer, so nothing
  open in the Inbox said the producer waited for the owner; the only trace was an unnamed 1 on the badge.

## What Changes

- **The badge counts what the Inbox shows.** The Inbox gets an "Alerts" section listing active task alerts and runs to
  inspect (task, project, summary, since when, link to the task, the clearing action when there is one), backed by a
  list API (`GET /alerts`, through the web BFF and the TUI client). The badge total and the page total are computed
  from the same items, and a test keeps them equal.
- **No "no progress" alert for a task that waits on someone.** `no_progress` skips tasks whose state says who they
  wait for (parked `wait`, a wait on the owner or another agent, a pinned or queued wait): the wait itself is shown
  where it belongs. A run that ends with no progress and no recorded wait still raises the alert.
- **A promised owner answer stays open.** When the owner answers a question with "Other — I'll write details" (or any
  answer that defers the details), the question stays open in the Inbox as "details promised" until the owner sends
  them or withdraws, and the waiting task's state says it waits for the owner's details. The agent no longer has to
  rerun to find out it still waits.

Not in scope: new alert kinds; Telegram changes beyond listing the same open item.

## Owner decisions (2026-11-12)

1. **Where alerts live: a section in the Inbox.** One place for what waits on the owner; no separate page or second
   badge. Rejected: a separate Alerts page; dropping alerts from the badge.
2. **Alerts meant for leads: shown to the owner too, marked "with <lead>".** The owner sees everything the badge
   counts; the mark says who is expected to act. Rejected: only the owner's own alerts.
3. **Deferred answer: an explicit "I'll send details" option in question forms.** Rejected: detecting "Other" with
   an empty text (the 2026-11-08 answer had text, "I'll write details", and would not have been caught).
4. **The waiting task wakes on the details, not on a timer.** A task waiting for promised details does not rerun to
   check; it resumes when the owner sends the details or withdraws. If decision-waits lands first, this uses its
   wake-on-record mechanism.

## Impact

Core: alerts list API; `NoProgress` excludes waiting tasks; a deferred-answer state on questions and the waiting
task's reason. Web: Inbox "Alerts" section, badge from the same list, question card "details promised". TUI: the same
list. Specs: `messaging`, `run-recovery`. Related: decision-approval-ux, inbox-chat-replies, park-watchdog,
throughput-signals.
