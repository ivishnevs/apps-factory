## 1. Core owns the items

- [ ] 1.1 Review items: a task entering review with a person as reviewer gets a `review` item in that person's topics;
      it settles when the review ends (accepted, sent back, reassigned, cancelled).
- [ ] 1.2 Task view `person_items` (topic, kind, person, actions) for every open item of the task.
- [ ] 1.3 No alert for a cause that has an open person item on the same task.
- [ ] 1.4 Tests: the 2026-11-15 case with fixtures (task in review with an agent reviewer, escalated to the owner →
      one escalation item, no review item, `person_items` has exactly the escalation); a person-reviewed task → one
      review item; settling clears it.

## 2. Surfaces render Core's items

- [ ] 2.1 Inbox reviews lane from Core review items; the `clearer` filter removed.
- [ ] 2.2 Task page: `person_items` rendered with the Inbox card and an "Open in Inbox" link; answers go through the
      same action.
- [ ] 2.3 Home "Needs you", "for you" stat, board "you" view, project counts from `person_items`; badge parity kept.
- [ ] 2.4 Headline and review buttons from `person_items`; `needsOwner` and humanize's owner cases removed.
- [ ] 2.5 Telegram: no raw line for a cause that has a topic item.
- [ ] 2.6 TUI reads the same items.
- [ ] 2.7 Web tests: one escalated task in review appears once in the Inbox, once on its task page (linked), not in
      reviews; the badge equals the Inbox.

## 3. Texts

- [ ] 3.1 Escalation title "<task title>: <cause in people words>"; group key unchanged.
- [ ] 3.2 People surfaces never show the raw escalation body.
- [ ] 3.3 Template completeness test over every escalation reason and answer (fixes `event_wait_deadline`,
      `wait_cycle`, `wake`).

## 4. Docs

- [ ] 4.1 Owner web docs: where items come from; the Inbox and task page show the same card.
