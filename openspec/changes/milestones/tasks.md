## 0. Decide

- [ ] 0.1 Owner accepts or changes D1–D9 in proposal.md; update the spec to the chosen options (it still allows
      BACKLOG for outside work and has no Ongoing milestone).

## 1. Core

- [ ] 1.1 Milestones table and API (project, title, goal, done_when, order, status, changes); task milestone link
      through the change or direct; `off_milestone` marker with reason; tests.
- [ ] 1.2 Lead tools `milestone_create`, `milestone_update`, `milestone_report`; activation, deferral and reorder
      only through an owner decision; tests.
- [ ] 1.3 Progress view: done / open / blocked counts, blockers, created vs closed per day, ETA estimate; tests.

## 2. Owner surfaces

- [ ] 2.1 Home and board: active milestone panel per project, next milestone, board filter, task chip.
- [ ] 2.2 Milestone report in the inbox and Telegram; accept or send back with notes; pick the next one.
- [ ] 2.3 TUI: milestone line on the project view.

## 3. Agents

- [ ] 3.1 Producer and lead prompts: keep milestones current, create tasks inside the active milestone or mark
      `off_milestone`, send the milestone report, ask the owner before the next milestone.

## 4. Verify

- [ ] 4.1 gnome-arena: the producer creates M4, M5, M5b, Cinderforge, M7 and prototype 2.0 end; the owner activates
      one; the home page shows its progress and blockers; a task created outside it shows the marker; closing it sends
      the report.
