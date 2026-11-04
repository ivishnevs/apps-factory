Done (owner 2026-11-04). Order: the web API inside Core, then the team builder pages.

## 1. Specs
- [x] 1.1 Proposal, design (defaults D1–D4, open questions), delta specs `owner-web`, `team-builder`; `openspec validate --strict`.

## 2. One service
- [x] 2.1 `internal/web` in-memory transport to a handler; Core mounts `/api/*`, `/auth/*`, `/api/core/*`; `FACTORY_WEB_LOCAL=1` token mode without sign-in. Done: `web.HandlerTransport`, `withWebAPI` in `factory serve` (`/auth/*` is Core's own route already); `TestWebAPIInCore`.
- [x] 2.2 `factory web` demo only; Next.js rewrites to Core (`FACTORY_URL`); compose and docs. Done: `owner-ui.md`, `architecture.md`, `development.md`, `compose.yaml` (`FACTORY_WEB_LOCAL`).

## 3. Team builder (team-builder 5.1–5.3)
- [x] 3.1 Setup wizard: team from a preset (or keep the current one) → agents → missing secrets → node → first project. Done: `web/src/app/c/[company]/setup/page.tsx`.
- [x] 3.2 New team from the preset gallery; add agent (preset or custom); agent form with field errors and skills. Done: `web/src/components/app/team/builder.tsx` (`NewTeamSheet`, `AddAgentSheet`, `AgentFormSheet`; `ApiError.field`).
- [x] 3.3 Team pages: agent cards with edit/delete, preset update hints, setup banner. Done: cards with "Edit agent", update hint in the form, `SetupBanner` on Teams and a team's page; `owner-ui.md`.

## 4. Verification
- [x] 4.1 `make check` green; Go tests of the in-process web API (session passthrough, token mode only with `FACTORY_WEB_LOCAL`), `tsc`, lint and build of the web app; the pages checked in a browser against a local Core. Done: `make check` green; `TestWebAPIInCore`; `tsc`, lint and `next build`; checked in a browser against the local Core (wizard, gallery, a field error on a taken team name, agent form, add agent) without saving to the live catalog. Found and fixed: null lists in the preset gallery broke every page.