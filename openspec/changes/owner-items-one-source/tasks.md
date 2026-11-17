## 1. Core owns the items

- [x] 1.1 Review items: a task entering review with a person as reviewer gets a `review` item in that person's topics;
      it settles when the review ends (accepted, sent back, reassigned, cancelled).
- [x] 1.2 Task view `person_items` (topic, kind, person, actions) for every open item of the task.
- [x] 1.3 No alert for a cause that has an open person item on the same task.
- [x] 1.4 Tests: the 2026-11-15 case with fixtures (task in review with an agent reviewer, escalated to the owner →
      one escalation item, no review item, `person_items` has exactly the escalation); a person-reviewed task → one
      review item; settling clears it.

## 2. Surfaces render Core's items

- [x] 2.1 Inbox reviews lane from Core review items; the `clearer` filter removed.
- [x] 2.2 Task page: `person_items` rendered with the Inbox card and an "Open in Inbox" link; answers go through the
      same action. (It renders the task's topics with the Inbox's `TopicCard`.)
- [x] 2.3 Home "Needs you", "for you" stat, board "you" view, project counts from `person_items`; badge parity kept.
- [x] 2.4 Headline and review buttons from `person_items`; `needsOwner` and humanize's owner cases removed.
- [x] 2.5 Telegram: no raw line for a cause that has a topic item.
- [x] 2.6 TUI reads the same items.
- [x] 2.7 Web tests: one escalated task in review appears once in the Inbox, once on its task page (linked), not in
      reviews; the badge equals the Inbox. Note: the web has no test runner; the rule is tested at the API the web
      renders (`internal/core/person_items_test.go` TestEscalatedReviewOnce: one topic, one waiting item, `person_items`
      linked to it, no owner review, badge parity), and the web change is checked with tsc and lint.

## 3. Texts

- [x] 3.1 Escalation title "<task title>: <cause in people words>"; group key unchanged.
- [x] 3.2 People surfaces never show the raw escalation body.
- [x] 3.3 Template completeness test over every escalation reason and answer (fixes `event_wait_deadline`,
      `wait_cycle`, `wake`).

## 4. Docs

- [x] 4.1 Owner web docs: where items come from; the Inbox and task page show the same card.

## Assumptions (decisions not settled in the proposal)

- A review item waits for its person reviewer: `owner`, or `member:USER` when the task's `review_by` names a member.
  An item for `owner` belongs to the people's Inbox and counts as "for you" for whoever is signed in; a member's item
  counts only for that member.
- A review item does not wait while an active park holds its task (as Core's existing owner-reviews count already
  ruled); the park's own item is what people see. Its item row is kept and settles (state `settled`) when the task
  leaves a person's review.
- An escalation's topic is titled "<task>: <cause>" while it holds one task; when another task's escalation of the
  same agent and reason joins it, the title becomes "<agent>: <group>" and each item's note names its task and cause.
- The Inbox's reviews lane lists Core's review items in topics that hold nothing else for you; a review beside other
  items stays in its topic's card (with its Accept / Return buttons), so nothing is listed twice.
- "No alert for the same cause" is applied per task: a task with an open person item is left out of `/alerts`.
