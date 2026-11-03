## Context

See proposal. Related: `postgres-tenancy` (accounts own companies and users; memberships `owner|member`; every row
company-scoped), `company-self-service` (stored catalogs, `POST /companies`, owner tokens `fot_…`, Team and
Companies pages), `owner-web` (its out of scope: sign-in, several companies and users), `core-secret-store`
(Dex connector secrets may move there later).

## Decisions (owner 2026-11-03)

### D1. Tenant is the company; a user has many companies
Data, catalog, skills, projects and nodes belong to a company (unchanged). A user reaches companies through
`memberships`. On first sign-in a user gets a personal account (`accounts`) and becomes a user of it; every company
they create is a company of that account with them as `owner`. The schema's rule that a membership stays inside
one account is kept; cross-account invitations are out of scope (they need that FK relaxed, a later change).

### D2. Sign-in through Dex (OIDC)
Dex (Apache 2.0, CNCF) runs next to Core (`compose.yaml`), stores its state in the same Postgres (own database),
and federates Google (`type: google`) and GitHub (`type: github`, which is OAuth2, not OIDC: Dex turns it into
OIDC). Core knows only one OIDC issuer: discovery, JWKS, `coreos/go-oidc` + `x/oauth2`. Adding a provider is a
Dex connector and a button, no Core code. Swapping Dex for Zitadel/Keycloak later (per-tenant SSO) is a change
of issuer. Core passes `connector_id` so Dex's own provider page is skipped: the user clicks "Sign in with
Google" on our page and lands at Google.

Flow: `GET /auth/login?provider=google` → Core stores `state`, `nonce`, PKCE verifier in a short-lived signed
cookie → redirect to Dex `/auth` → provider → Dex → `GET /auth/callback?code&state` → Core checks `state`,
exchanges the code with the verifier (server to server), verifies the ID token (signature by JWKS, `iss`, `aud`,
`exp`, `nonce`) → upserts the user → creates a session → redirect to the last used company. IdP tokens are not
kept.

### D3. Identity is (issuer subject), not email
`identities (user_id, provider, subject, email, created_at)`, unique `(provider, subject)`; `provider` is Dex's
connector id from the token's `federated_claims`. A new identity whose email is verified (`email_verified`) and
equals an existing user's email is linked to that user; otherwise a new user is created. `users.email` stays
unique.

### D4. Server-side sessions
`sessions (id_hash, user_id, created_at, expires_at, last_seen_at, revoked_at)`: the cookie carries a random
id (HttpOnly, Secure, SameSite=Lax, path `/`); only its hash is stored. Sliding expiry 30 days, absolute 90 days;
sign-out revokes. State-changing requests from a session need a CSRF header (`X-Factory-CSRF` matching a
non-HttpOnly cookie), tokens do not.

### D5. Company in every request
The web UI routes are `/c/{company}/…`; owner API calls from the web carry `X-Factory-Company`. `companyFrom`
resolves: run credential / node token / owner token → their company (unchanged; a header naming another company
is refused); session → the header's company if the user has a membership there, else 404 (no existence leak).
Live streams subscribe per company. The last used company is remembered per user
(`users.last_company_id`).

### D6. Who may do what
`owner`: everything of the company (catalog, skills, nodes, tokens, members later). `member`: work with tasks,
chats, approvals; read the catalog. The operator token keeps operator-only endpoints (prices, built-in content).
Users create companies themselves (`POST /companies` with a session); the operator path stays for support.

## Risks / Trade-offs

- Dex connectors are static configuration: per-client SSO needs another IdP (D2 keeps the swap cheap).
- Account per user (D1) means a company cannot yet be shared with another person's account; the schema supports
  it once the membership FK is relaxed.
- Dex is one more service to update; its storage and keys live in our Postgres and backups.
