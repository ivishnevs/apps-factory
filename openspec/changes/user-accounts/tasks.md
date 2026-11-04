Planned (owner 2026-11-03: D1–D7 confirmed). Not started. No migration (not released). Order: storage, OIDC client
and Dex, sessions and company resolution, roles, company at sign-up, web UI. Can run in parallel with
`company-teams`.

## 1. Decide
- [x] 1.1 Owner confirms the company as tenant, one company per user for now, several behind a setting (D1, D2).
- [x] 1.2 Owner confirms Dex with Google and GitHub connectors (D3).
- [x] 1.3 Owner confirms roles owner/admin/member and no operator role in the web (D7).

## 2. Specs and docs
- [x] 2.1 Delta specs `user-accounts`, `postgres-tenancy`; `openspec validate --strict`.
- [ ] 2.2 Docs: `catalog.md` (company resolution by session, roles), `owner-ui.md` (sign-in, routes, rename, selector
  setting), `development.md` (Dex in compose, OAuth apps, env), `architecture.md`.

## 3. Storage
- [x] 3.1 Schema: `identities`, `sessions`, `users.name`, `users.last_company_id`, memberships role
  `owner|admin|member` (migration `0009_user_accounts`, numbered like the other changes' migrations instead of an
  in-place baseline edit); store functions for upsert by identity (D4), account and company
  at first sign-in, sessions (create, touch, revoke), memberships of a user, company rename. Done:
  `internal/storage/users.go` (`SignIn`, `CreateSession`/`AuthenticateSession`/`RevokeSession` with only the
  SHA-256 of the `fws_` id stored, `Role`, `SetLastCompany`, `CreateUserCompany`, `RenameCompany`),
  `owner_tokens.created_by`; `TestSignInCreatesAccountAndCompany`, `TestSessions`.

## 4. Sign-in
- [ ] 4.1 Dex in `compose.yaml` (Postgres storage, static client `factory-core`, Google and GitHub connectors from
  env); `.env.example` entries; local development through Dex's mock connector.
- [ ] 4.2 `internal/auth`: discovery, PKCE login, callback, ID token verification, `connector_id` per button.
- [ ] 4.3 Core: `/auth/login`, `/auth/callback`, `/auth/logout`, `GET /me` (user, companies, roles, last company);
  CSRF.

## 5. Company context and roles
- [ ] 5.1 `companyFrom` by session + `X-Factory-Company` with a membership check (404 otherwise); tokens refuse a
  foreign company header; live streams per requested company.
- [ ] 5.2 Company at first sign-in; rename; `POST /companies` with a session only when `FACTORY_MULTI_COMPANY` is on;
  the operator path stays for support.
- [ ] 5.3 Roles on owner routes (D7); owner tokens issued by owners and admins record their author.

## 6. Web UI
- [ ] 6.1 Sign-in page (provider buttons), sign-out, session expiry handling.
- [ ] 6.2 `/c/{company}/…` routes for every page; company settings (rename); selector and "new company" only with
  the setting on.

## 7. Verification
- [ ] 7.1 `make check` green plus NATS tests with `-p 1`; tests: callback state/nonce refusal, identity linking by
  verified email only, company at first sign-in, session revocation, membership 404, token with a foreign company
  header, member refused on catalog save, second company refused with the setting off.
