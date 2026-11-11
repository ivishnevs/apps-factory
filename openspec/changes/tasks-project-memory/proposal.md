Status: implemented 2026-11-11, asked for by the owner ("when I pick a project, Tasks does not keep the project I
picked before").

## Why

The Tasks page keeps its filters only in the address (`?project=`). Coming back to Tasks from the sidebar, or after
opening a task, starts again on All projects, so an owner who works on one product picks it again every time.

## What Changes

- **Tasks remembers the picked project.** Picking a project on Tasks, or arriving with `?project=` from a link, stores
  it for that company in the browser (`localStorage`, key `factory.tasks.project.<company>`). Opening Tasks without
  `?project=` restores it. Picking All projects is remembered too, and a project that no longer exists or is archived
  is forgotten instead of restored.
- The address stays the source of truth: a link with `?project=` wins over the remembered pick.

## Impact

- `web/src/app/c/[company]/tasks/page.tsx`; `docs/owner-ui.md` (web Tasks).
- No API, Core or TUI change.
