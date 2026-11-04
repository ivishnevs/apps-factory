Status: done (owner 2026-11-04: the web is reworked last, after the backend changes, "как вишенка на торте").

## Why

- The web UI talks to Core through a separate Go process (`factory web`) that once held the owner token on the
  owner's Mac. In the SaaS, Core already signs people in and checks their role per request (`user-accounts`): the
  extra process is one more service to deploy, a second auth path and per-session caches, with nothing left to hide
  from the browser.
- `team-builder` delivered the API of presets, teams, agent forms and the setup status; its wizard and forms (5.x)
  were left for this change so they are built once, on the final web.

## What Changes

- **Core serves the web API itself:** `/api/*` (the screens' composite reads, actions and streams of
  `internal/web`) and `/auth/*` run inside Core; their calls to Core's routes go to Core's handler in memory, with
  the person's session, CSRF and company. The Next.js app sends `/api` and `/auth` to Core. `factory web` keeps only
  its demo mode (`--demo`, `--fixture`). On the owner's machine without sign-in, Core serves the web API as the owner
  only when `FACTORY_WEB_LOCAL=1` (loopback setups).
- **Team builder in the web:** a setup wizard (team from a preset → agents → missing secrets → node → first
  project), "New team" from the preset gallery, "Add agent" (preset or custom) and an agent form (name, description,
  reports_to, can_message, runtime, prompt, skills) with field errors, agent cards with edit/delete, preset update
  hints and a setup banner on team pages; the YAML editor stays.

## Out of scope

A visual redesign of every screen (open questions for the owner are kept in the change's design), billing,
Fly Sprites nodes.

## Impact

`internal/core` (web API mounted in Core), `internal/web` (in-memory transport), `cmd/factory` (`web` demo only,
`serve` mounts the web API), `web/` (rewrites to Core, team builder pages), `compose.yaml`, docs `owner-ui.md`,
`development.md`, `catalog.md`.
