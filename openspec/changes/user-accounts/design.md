## Context

See proposal. Related: `postgres-tenancy` (accounts own companies and users; memberships; every row
company-scoped), `company-self-service` (stored catalogs, `POST /companies`, owner tokens `fot_…`), `owner-web`
(its out of scope: sign-in, several companies and users), `company-teams` (teams inside the company),
`node-identity` (nodes enrolled by the company's owner or admin), `core-secret-store` (secrets for owner and admin).

## Decisions (owner 2026-11-03)

### D1. The company is the tenant; one company per user for now
The company owns every shared resource: nodes, quota keys, secrets, skills, people, catalog. Different products
run as teams inside it (`company-teams`). On first sign-in a user gets a personal account (`accounts`) and one
company of it, named after the user and renamable, with the user as `owner`. A membership stays inside one account
(schema rule kept); cross-account invitations are out of scope.

### D2. Several companies by design, switched off
Every route and request names its company (D6), so several companies per user need no further model change. Creating
another company (`POST /companies` with a session) and the company selector in the header are enabled by
`FACTORY_MULTI_COMPANY=1`; off by default. A second company is a hard boundary: its own nodes, secrets, skills and
quota keys (a node serves one company, `node-identity`).

### D3. Sign-in through Dex (OIDC)
Dex (Apache 2.0, CNCF) runs next to Core (`compose.yaml`), stores its state in the same Postgres (own database),
and federates Google (`type: google`) and GitHub (`type: github`, which is OAuth2, not OIDC: Dex turns it into
OIDC). Core knows only one OIDC issuer: discovery, JWKS, `coreos/go-oidc` + `x/oauth2`. Adding a provider is a
Dex connector and a button, no Core code. Swapping Dex for Zitadel/Keycloak later (per-tenant SSO) is a change
of issuer. Core passes `connector_id` so Dex's own provider page is skipped.

Flow: `GET /auth/login?provider=google` → Core stores `state`, `nonce`, PKCE verifier in a short-lived signed
cookie → redirect to Dex `/auth` → provider → Dex → `GET /auth/callback?code&state` → Core checks `state`,
exchanges the code with the verifier (server to server), verifies the ID token (signature by JWKS, `iss`, `aud`,
`exp`, `nonce`) → upserts the user → creates a session → redirect to the user's company. IdP tokens are not kept.

### D4. Identity is (provider, subject), not email
`identities (user_id, provider, subject, email, created_at)`, unique `(provider, subject)`; `provider` is Dex's
connector id from the token's `federated_claims`. A new identity whose email is verified (`email_verified`) and
equals an existing user's email is linked to that user; otherwise a new user is created. `users.email` stays
unique.

### D5. Server-side sessions
`sessions (id_hash, user_id, created_at, expires_at, last_seen_at, revoked_at)`: the cookie carries a random
id (HttpOnly, Secure, SameSite=Lax, path `/`); only its hash is stored. Sliding expiry 30 days, absolute 90 days;
sign-out revokes. State-changing requests from a session need a CSRF header (`X-Factory-CSRF` matching a
non-HttpOnly cookie), tokens do not.

### D6. Company in every request
The web UI routes are `/c/{company}/…`; owner API calls from the web carry `X-Factory-Company`. `companyFrom`
resolves: run credential / node key / owner token → their company (unchanged; a header naming another company is
refused); session → the header's company if the user has a membership there, else 404 (no existence leak). Live
streams subscribe per company. The last used company is remembered per user (`users.last_company_id`).

### D7. Roles
`memberships.role` is `owner`, `admin` or `member` (the schema's check is changed in place; not released, no
migration).
- `owner`: everything, including roles of others and renaming or deleting the company.
- `admin`: catalog (teams, agents, profiles, policies, prompts), skills, secrets, nodes (enroll, approve, revoke),
  projects, owner tokens.
- `member`: tasks, chats, approvals, projects' work; reads the catalog; sees secret names only.
The operator has no web role: the operator token keeps operator-only routes (prices, built-in content, support).

## Risks / Trade-offs

- Dex connectors are static configuration: per-client SSO needs another IdP (D3 keeps the swap cheap).
- Account per user (D1) means a company cannot yet be shared with another person's account; the schema supports
  it once the membership FK is relaxed.
- Dex is one more service to update; its storage and keys live in our Postgres and backups.
