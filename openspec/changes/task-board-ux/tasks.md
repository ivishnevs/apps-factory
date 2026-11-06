## 1. Decisions (owner)

- [x] 1.1 Task panel postponed; the task page stays (owner 2026-11-06).
- [x] 1.2 Priority scale: not now (owner 2026-11-06).
- [x] 1.3 Waiting grouping: not now; columns renamed instead (owner 2026-11-06).
- [ ] 1.5 Name of the "needs someone to act" column now that Waiting is Blocked (Needs action proposed), or fold it
      into Blocked.
- [x] 1.4 A Blocked column for tasks that need someone to act; Waiting only for what comes in its turn (owner
      2026-11-06).

## 2. Task over the board (postponed)

- [ ] 2.1 Intercepted route `/tasks/{id}` opens the panel over the board; reload/share opens the full page.
- [ ] 2.2 Panel renders from the board's data at once, fills details when loaded; prefetch on hover.
- [ ] 2.3 Board state (open columns, view, filters, scroll) survives opening and closing a task.
- [ ] 2.4 Previous/next in the panel within the column; Esc closes; keyboard navigation on the board.

## 3. Words and reasons

- [ ] 3.1 Core: structured capacity reason (agent limit, project writer, node slots, unpushed work); tests.
- [ ] 3.2 Web: "AGENT runs N of N" with links; "All nodes busy" only for full nodes.
- [ ] 3.3 One status vocabulary (column names) on card, panel, page, Home and Telegram; the task header badge shows
      the column, not the engine status.
- [ ] 3.5 Blocked column: one shared classification (Core verdict or web model) of "needs someone to act" vs "comes
      in its turn"; board, phone bar, Home; tests for every reason.
- [ ] 3.4 Rename columns: Ready → To do, Waiting → Blocked, Running → In progress (web, TUI, Telegram, Home);
      engine statuses unchanged.

## 4. Board content

- [ ] 4.1 (not now) Waiting grouped by what it waits on.
- [ ] 4.2 Running cards with run time, last step and node.
- [ ] 4.3 Header actions untruncated; UX pass on phone and desktop (focus, contrast, touch targets, empty/loading).
