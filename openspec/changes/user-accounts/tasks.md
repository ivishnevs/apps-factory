Done (owner 2026-11-03: D1–D7 confirmed). Migration `0010_user_accounts`. Order: storage, OIDC client
and Dex, sessions and company resolution, roles, company at sign-up, web UI. Can run in parallel with
`company-teams`.

## 1. Decide
- [x] 1.1 Owner confirms the company as tenant, one company per user for now, several behind a setting (D1, D2).
- [x] 1.2 Owner confirms Dex with Google and GitHub connectors (D3).
- [x] 1.3 Owner confirms roles owner/admin/member and no operator role in the web (D7).

## 2. Specs and docs
- [x] 2.1 Delta specs `user-accounts`, `postgres-tenancy`; `openspec validate --strict`.
- [x] 2.2 Docs: `catalog.md` (company resolution by session, roles), `owner-ui.md` (sign-in, routes, rename, selector
  setting), `development.md` (Dex in compose, OAuth apps, env), `architecture.md`.

## 3. Storage
- [x] 3.1 Schema: `identities`, `sessions`, `users.name`, `users.last_company_id`, memberships role
  `owner|admin|member` (migration `0010_user_accounts`, numbered like the other changes' migrations instead of an
  in-place baseline edit); store functions for upsert by identity (D4), account and company
  at first sign-in, sessions (create, touch, revoke), memberships of a user, company rename. Done:
  `internal/storage/users.go` (`SignIn`, `CreateSession`/`AuthenticateSession`/`RevokeSession` with only the
  SHA-256 of the `fws_` id stored, `Role`, `SetLastCompany`, `CreateUserCompany`, `RenameCompany`),
  `owner_tokens.created_by`; `TestSignInCreatesAccountAndCompany`, `TestSessions`.

## 4. Sign-in
- [x] 4.1 Dex in `compose.yaml` (Postgres storage, static client `factory-core`, Google and GitHub connectors from
  env); `.env.example` entries; local development through Dex's mock connector. Done: `deploy/dex/config.yaml`
  (gomplate, rendered by the Dex image: Google/GitHub only when their OAuth app is set, `DEX_MOCK_CONNECTOR=1`),
  services `dex-db` (creates the `dex` database once) and `dex` (v2.43.1); Core reaches it as
  `FACTORY_OIDC_INTERNAL_URL`. `TestDexMockConnector` signs in through a real Dex (`DEX_TEST_ISSUER`).
- [x] 4.2 `internal/auth`: discovery, PKCE login, callback, ID token verification, `connector_id` per button.
  Done: lazy discovery, login state/nonce/verifier in an HMAC-signed 10-minute cookie, identity from
  `federated_claims`, local return paths only; `internal/auth/authtest` fake issuer; `TestSignIn`.
- [x] 4.3 Core: `/auth/login`, `/auth/callback`, `/auth/logout`, `GET /me` (user, companies, roles, last company);
  CSRF. Done: `internal/core/sessions.go` (also `GET /auth/providers`), cookies `factory_session` (HttpOnly) and
  `factory_csrf` matched by `X-Factory-CSRF`; `factory serve` reads `FACTORY_OIDC_*`, `FACTORY_PUBLIC_URL`.

## 5. Company context and roles
- [x] 5.1 `companyFrom` by session + `X-Factory-Company` with a membership check (404 otherwise); tokens refuse a
  foreign company header; live streams per requested company. Done: `sessionGateway`; the operator token may name
  an existing company (support); sessions never reach node routes or operator routes; `TestSessionSignIn`.
- [x] 5.2 Company at first sign-in; rename; `POST /companies` with a session only when `FACTORY_MULTI_COMPANY` is on;
  the operator path stays for support. Done: catalog from `FACTORY_SIGNUP_TEMPLATE` (default `solo`), redirect to
  `/c/<company>/setup`; `GET/PATCH /company`; `TestMultiCompany`.
- [x] 5.3 Roles on owner routes (D7); owner tokens issued by owners and admins record their author. Done:
  `requiredRole` (owner: rename and roles; admin: catalog, skills, secrets, nodes, projects, owner tokens,
  settings, kill switch; member: the rest and reads), owner tokens act as admin, catalog versions name
  `user:<id>`; members see secret names only (`namesOnly` over every `/secrets` answer); `TestRoles`,
  `TestSecretNamesOnly`. Open: whether approving catalog changes needs owner/admin (pending decision).

## 6. Web UI
- [x] 6.1 Sign-in page (provider buttons), sign-out, session expiry handling. Done: `factory web --sessions` (no token;
  relays each person's session, CSRF and company; clients and caches per company and session,
  `internal/web/access.go`, `TestSessionMode`), `/auth/*` and `/api/core/*` proxied; `/signin`, sign-out in the
  company menu and Settings, a 401 returns to `/signin?return=…&expired=1`. Checked end to end in a browser against
  a real Dex (mock connector): sign-in, new company, rename, a foreign company, sign-out.
- [x] 6.2 `/c/{company}/…` routes for every page; company settings (rename); selector and "new company" only with
  the setting on. Done: pages under `web/src/app/c/[company]`, `Link`/`useRouter` put app paths under the company,
  `X-Factory-Company` on every call (cookie for images and streams), "This company is not available" for a 404,
  Settings page, company menu with the switcher, `/` opens the last company.

## 7. Verification
- [x] 7.1 `make check` green plus NATS tests with `-p 1`; tests: callback state/nonce refusal, identity linking by
  verified email only, company at first sign-in, session revocation, membership 404, token with a foreign company
  header, member refused on catalog save, second company refused with the setting off. Done: `TestSignIn`,
  `TestForgedCallback`, `TestSignInCreatesAccountAndCompany`, `TestSessionSignIn`, `TestRoles`, `TestMultiCompany`.
