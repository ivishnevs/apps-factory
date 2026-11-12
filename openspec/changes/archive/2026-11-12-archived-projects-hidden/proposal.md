Status: implemented 2026-11-12, asked for by the owner ("why does Home still show 12 projects?").

## Why

Nine of tvorog's twelve projects were archived on 2026-11-11. The Projects page folds them and Tasks hides them, but
Home still listed all twelve, the Chat page kept an owner chat with the lead for each of them, and the new-task form
offered them as projects.

## What Changes

- Home's Projects section lists only active projects (its count too); archived ones stay on the Projects page.
- The Chat page has no chats for archived projects.
- The new-task form offers only active projects (an archived one only when it is the page's preselected project).

## Impact

- `web/src/components/app/home/sections.tsx`, `web/src/components/app/chat/use-chats.ts`,
  `web/src/components/app/tasks/new-task.tsx`. No API change.
