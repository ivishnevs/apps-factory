## 1. Decisions (owner)

- [ ] 1.1 Side panel or centred modal for the task (side panel recommended).
- [ ] 1.2 Priority: show rank within the project, or a fixed scale.
- [ ] 1.3 Waiting: grouped sections or a filter; dependency waits collapsed by default.

## 2. Task over the board

- [ ] 2.1 Intercepted route `/tasks/{id}` opens the panel over the board; reload/share opens the full page.
- [ ] 2.2 Panel renders from the board's data at once, fills details when loaded; prefetch on hover.
- [ ] 2.3 Board state (open columns, view, filters, scroll) survives opening and closing a task.
- [ ] 2.4 Previous/next in the panel within the column; Esc closes; keyboard navigation on the board.

## 3. Words and reasons

- [ ] 3.1 Core: structured capacity reason (agent limit, project writer, node slots, unpushed work); tests.
- [ ] 3.2 Web: "AGENT runs N of N" with links; "All nodes busy" only for full nodes.
- [ ] 3.3 One status vocabulary (column names) on card, panel, page, Home and Telegram.
- [ ] 3.4 Priority mark with a scale per decision 1.2.

## 4. Board content

- [ ] 4.1 Waiting grouped by what it waits on per decision 1.3.
- [ ] 4.2 Running cards with run time, last step and node.
- [ ] 4.3 Header actions untruncated; UX pass on phone and desktop (focus, contrast, touch targets, empty/loading).
