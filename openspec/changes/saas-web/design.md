## Context

See proposal. Related: `owner-web` (screens, materials, actions), `user-accounts` (sessions, roles, company per
request), `team-builder` (presets, catalog agent routes, setup status), `company-teams` (team pages).

## Decisions (defaults taken 2026-11-04; the owner may revise them)

### D1. One service: Core serves the web API in process
Core mounts `internal/web`'s handler for `/api/*` and its proxy for `/auth/*` and `/api/core/*`. The web package's
Core client uses a transport that calls Core's own handler in memory (streams flow through a pipe), carrying the
browser's session cookie, CSRF header and company exactly as before, so Core's session gateway decides as for any
request. Nothing about the browser contract changes: the same `/api/...` paths, the same `X-Factory-Web` header on
changes.

### D2. Local owner without sign-in
Without an OIDC issuer the web API answers only when `FACTORY_WEB_LOCAL=1` and acts with Core's owner token (the
former `factory web` token mode); Core then must listen on loopback only. With sign-in configured the token is never
used for the web.

### D3. `factory web` keeps demo mode
`factory web --demo|--fixture` serves fixtures for UI development without Core; the live and session modes are gone.

### D4. Team builder pages on the existing shell
The wizard is `/c/{company}/setup`; "New team" lives on Teams; "Add agent", the agent form and cards with
edit/delete live on a team's page. All writes use `team-builder`'s routes with the catalog version the page read; a
409 asks to reload; field errors land at their field.

## Open questions for the owner (do not block this change)

- Visual direction of the SaaS web (brand, density, dark/light default, landing page before sign-in).
- Navigation: keep the company → teams → projects sidebar, or a project-first home.
- Whether members (not only owners and admins) see costs.
- A mobile-first redesign beyond today's responsive pages.
