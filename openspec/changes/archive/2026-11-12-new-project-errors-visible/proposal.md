Status: implemented 2026-11-12, reported by the owner ("can't add a project": the Create button seemed to do nothing).

## Why

The new-project sheet is longer than the screen. Its field errors (name, ID, team…) appear under fields at the top,
while the owner presses Create at the bottom, so a refused form looked like a dead button.

## What Changes

- On submit with errors, the sheet scrolls the first refused field into view and focuses it.

## Impact

- `web/src/components/app/project/new-project.tsx`. No API change.
