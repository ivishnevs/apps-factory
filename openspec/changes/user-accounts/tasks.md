Planned (owner 2026-11-03: D1–D6 confirmed). Not started. Order: storage, OIDC client and Dex, sessions and
company resolution, self-service companies, web UI.

## 1. Decide
- [x] 1.1 Owner confirms the company as tenant with several companies per user and a company selector (D1, D5).
- [x] 1.2 Owner confirms Dex with Google and GitHub connectors (D2).

## 2. Specs and docs
- [x] 2.1 Delta spec `user-accounts`; `openspec validate --strict`.
- [ ] 2.2 Docs: `catalog.md` (company resolution by session), `owner-ui.md` (sign-in, selector, routes),
  `development.md` (Dex in compose, OAuth apps, env), `architecture.md`.

## 3. Storage
- [ ] 3.1 Migration: `identities`, `sessions`, `users.name`, `users.last_company_id`; store functions for upsert by
  identity (D3), personal account on first sign-in, sessions (create, touch, revoke), memberships of a user.

## 4. Sign-in
- [ ] 4.1 Dex in `compose.yaml` (Postgres storage, static client `factory-core`, Google and GitHub connectors from
  env); `.env.example` entries; local development without real providers through Dex's mock connector.
- [ ] 4.2 `internal/auth`: discovery, PKCE login, callback, ID token verification, `connector_id` per button.
- [ ] 4.3 Core: `/auth/login`, `/auth/callback`, `/auth/logout`, `GET /me` (user, companies, last company); CSRF.

## 5. Company context
- [ ] 5.1 `companyFrom` by session + `X-Factory-Company` with a membership check (404 otherwise); tokens refuse a
  foreign company header; roles (D6) on owner endpoints.
- [ ] 5.2 Users create companies (`POST /companies` with a session, optional team preset from `team-builder`);
  owner tokens issued by a signed-in owner record their author.
- [ ] 5.3 Live streams subscribe per requested company.

## 6. Web UI
- [ ] 6.1 Sign-in page (provider buttons), sign-out, session expiry handling.
- [ ] 6.2 `/c/{company}/…` routes for every page; company selector in the header (companies, last used,
  "new company"); company creation page.

## 7. Verification
- [ ] 7.1 `make check` green plus NATS tests with `-p 1`; tests: callback state/nonce refusal, identity linking
  by verified email only, session revocation, membership 404, token with a foreign company header, two companies
  of one user side by side in the web API.
