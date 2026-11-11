Status: implemented 2026-11-11, asked for by the owner ("the tracely project is paused, but the UI shows no pause").

## Why

The owner paused tracely (and other projects) through the API. In the web UI the pause showed only on Nodes & control.
The Projects list and the project's page looked like a running project, and the owner could not tell why the CEO did
not act on tracely or where to resume it.

## What Changes

- **Projects list:** a paused project carries a Paused badge (its reason in the tooltip).
- **Project page:** a paused project shows a banner: what a pause means (no new work, no chat answers), the reason,
  who paused it and when, and Resume for owners and admins. A running project gets a Pause button next to its links.
- Nodes & control keeps the full list of pauses; the actions are the same `hold` / `release` owner actions.

## Impact

- `web/src/components/app/project/pause.tsx`, `web/src/app/c/[company]/projects/page.tsx`,
  `web/src/app/c/[company]/projects/[id]/page.tsx`; `docs/owner-ui.md`. No API or Core change.
