Status: planned (owner request 2026-11-03: SaaS users sign in on the website, own several companies and switch
between them; decisions D1–D6 confirmed the same day, see design.md). Not started.

## Why

Clients will use App Factory as a SaaS: they sign up on the website, create their companies and work there. Today
nobody signs in:

- The schema already models accounts, users, companies and memberships (`postgres-tenancy`,
  `0001_baseline.sql`), but no code creates a user from a sign-in, and `memberships` is never read for access.
- A request's company comes from its token only (`companyFrom`: the operator's `FACTORY_TOKEN` serves
  `FACTORY_COMPANY`, a company owner token `fot_…` its company, `docs/catalog.md`). Companies are created by the
  operator (`POST /companies`, operator token), and the owner token is shown once.
- The web UI (`owner-web`) has no sign-in and serves one company per token; there is no way to see or switch
  between several companies.

## What Changes

- **Sign-in through OIDC:** a Dex server next to Core federates Google and GitHub (more connectors by
  configuration). Core is an OIDC relying party (authorization code + PKCE), creates users and identities on first
  sign-in and keeps its own server-side web sessions.
- **Self-service companies:** a signed-in user creates companies in their own account and becomes their `owner`;
  a user may own and switch between any number of companies.
- **Company context per request:** the web UI addresses a company in the URL (`/c/{company}/…`), every owner API
  call names its company, and Core checks the user's membership. Tasks, chats, team, skills, projects, nodes and
  live streams are those of the selected company only.
- **Company selector** in the web UI header: the user's companies, the last one used, "new company".
- Owner tokens `fot_…` stay for CLI and automation, now issued by a signed-in owner (author recorded).
- Catalog versions and other authored records name the user (`user:<id>`).

## Out of scope

Inviting users into another account's company (cross-account membership), roles beyond `owner`/`member`,
per-tenant enterprise SSO (SAML/Okta configured by the client), password sign-in, billing.

## Impact

`internal/storage` (identities, sessions, membership queries), `internal/core` (auth endpoints, `companyFrom` by
session + requested company, company creation by users), new `internal/auth` (OIDC client), `compose.yaml`
(Dex), `internal/web` + `web/` (sign-in page, company selector, `/c/{company}` routes), docs `catalog.md`,
`owner-ui.md`, `development.md`, `architecture.md`.
