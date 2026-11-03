Status: planned (owner request 2026-11-03: SaaS users sign in on the website and get a company to set up; decisions
D1–D7 confirmed the same day, see design.md). Not started.

## Why

Clients will use App Factory as a SaaS: they sign up on the website and set up their company there. Today nobody
signs in:

- The schema already models accounts, users, companies and memberships (`postgres-tenancy`,
  `0001_baseline.sql`), but no code creates a user from a sign-in, and `memberships` is never read for access.
- A request's company comes from its token only (`companyFrom`: the operator's `FACTORY_TOKEN` serves
  `FACTORY_COMPANY`, a company owner token `fot_…` its company, `docs/catalog.md`). Companies are created by the
  operator (`POST /companies`, operator token).
- The web UI (`owner-web`) has no sign-in and serves one company per token.

## What Changes

- **Sign-in through OIDC:** a Dex server next to Core federates Google and GitHub (more connectors by
  configuration). Core is an OIDC relying party (authorization code + PKCE), creates users and identities on first
  sign-in and keeps its own server-side web sessions.
- **A company at sign-up:** the first sign-in creates the user's account and one company (renamable) with the user
  as `owner`. Teams, agents, projects, nodes, secrets and skills are set up inside it (`company-teams`,
  `team-builder`, `node-identity`, `core-secret-store`, `skill-registry`).
- **More companies are supported but switched off:** the company is in every request (`/c/{company}/…`, membership
  check), so a user with several companies works by design; creating another company and the company selector are
  behind a setting (`FACTORY_MULTI_COMPANY`), off for now.
- **Company roles** `owner`, `admin`, `member`. The operator has no web role: platform tasks (prices, built-in
  content, support) stay on the CLI with the operator token.
- Owner tokens `fot_…` stay for CLI and automation, issued by a signed-in owner or admin (author recorded).
- Catalog versions and other authored records name the user (`user:<id>`).

## Out of scope

Inviting people and changing roles (the model is fixed here, the flows come later), cross-account membership,
per-team permissions, per-tenant enterprise SSO (SAML/Okta configured by the client), password sign-in, billing.

## Impact

`internal/storage` (identities, sessions, roles, membership queries), `internal/core` (auth endpoints,
`companyFrom` by session + requested company, roles on owner routes, company at sign-up), new `internal/auth`
(OIDC client), `compose.yaml` (Dex), `internal/web` + `web/` (sign-in page, `/c/{company}` routes, rename company,
selector behind the setting), docs `catalog.md`, `owner-ui.md`, `development.md`, `architecture.md`.
