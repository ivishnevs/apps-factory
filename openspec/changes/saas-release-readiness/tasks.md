## 1. Production environment

- [ ] 1.1 Owner: production Railway environment, custom domains, OAuth apps (GitHub, Google); I set the internal
  secrets and the services as in `docs/deploy-railway.md`.
  **Blocker:** needs the owner's Railway, domain and OAuth accounts.
- [x] 1.2 Dex keeps its state in Postgres; the image creates its database (sandbox done 2026-11-05).
- [ ] 1.3 `FACTORY_SECRETS_KEY` backed up outside Railway; rotation procedure written down.
  **Blocker:** the backup is the owner's (outside Railway); the rotation procedure is not written yet
  (`docs/incident-runbook.md` points here and warns not to change the key without it).

## 2. Operations

- [ ] 2.1 Daily Postgres backups and a restore drill; blob volume backup.
  **Blocker:** needs the production Railway project (1.1) and a backup target the owner pays for.
- [ ] 2.2 Uptime checks (`/health`, sign-in) and error alerts.
  **Blocker:** needs a monitoring account (owner) and the production domain.
- [x] 2.3 Rate limits on public routes (auth, connector sign-in, installer, downloads, invitation preview); tests.
  Done 2026-11-12: `internal/core/ratelimit.go`, one check in front of every route: sign-in `/auth/*` (60 per
  address / 1,200 overall per minute), invitation preview (20 / 300), node enrollment and connection requests
  (10 / 120, the former enroll limiter), a waiting node's status polls (120 / 3,000), `/install.sh` and
  `/downloads/*` (30 / 600). Over the limit: 429, `Retry-After: 60`, `reason: rate_limited`. Routes with a
  credential are not counted. Tests: `ratelimit_test.go` (classes, per address, overall with forged addresses, the
  next minute, owner routes untouched).
- [x] 2.4 Incident runbook and a status page or status notes.
  Done 2026-11-12: `docs/incident-runbook.md` (first steps, where to look, common incidents, breach, data
  requests, write-up). Status notes: the operator sets one line (`PUT /status-note`, stored in `core_meta`);
  `/auth/providers` and `/me` carry it; the landing, the sign-in page and the app show it as a banner. Tests:
  `TestStatusNoteAndSupport`. No separate status page (it would go down with Core; see the runbook).

## 3. Security

- [x] 3.1 Security review of the public surface; findings fixed.
  Done 2026-11-12: `docs/security-review-2026-11.md`. Fixed with regression tests:
  - High: owner tokens act as admins (F1, an admin could make themselves owner through a token).
  - High: a delegated `factory_approval_decide` no longer deadlocks every agent call (F3).
  - Medium: approving a node connection needs the code typed from the terminal (F2, link phishing).
  - Medium: public skill fetches are cut at 256 MiB and limited to 2 at once (F4).
  - Medium: previews are bounded to 40 Mpx, one decode at a time (F5).
  - Medium: a storage quota per company, `FACTORY_COMPANY_STORAGE_GB` (F6).
  - Low: finished sync jobs refused (F7), `--` before clone URLs (F8), no symlinks in public sources (F9),
    `nosniff` on blob downloads (F10).

  14 Low or informational items stay open, listed in the review (O1–O14); none is High or Medium.
- [ ] 3.2 `connector-trust` done (runs confined, local policy, signed releases, Security page, pentest).
  Another change.
- [x] 3.3 Dependency update pass (Go, web, Claude worker, Dex image).
  Done 2026-11-12, `docs/dependency-updates-2026-11.md`: Go 1.26.11 and minor/patch module updates (govulncheck 61
  reachable findings to none), next 15.5.29 with a postcss override, worker transitive fixes (`npm audit --omit=dev`
  clean in both), Dex v2.46.1 (checked with Docker on Postgres, upgrade and mock sign-in). Majors deferred with
  reasons (next 16, recharts 3, TypeScript 7). Open decision: the Node.js 22.18.0 of the Claude single executable
  has six security releases since; recommended to move to 22.23.5 in its own change.

## 4. Legal and data

- [ ] 4.1 Owner: Terms of Service and Privacy Policy texts (I can draft them for a lawyer); pages and footer links.
  Drafted 2026-11-12: `docs/legal/terms-of-service.DRAFT.md`, `docs/legal/privacy-policy.DRAFT.md` (marked DRAFT,
  placeholders in brackets, cookie list and sub-processors from the code). **Blocker:** a lawyer's review and the
  owner's approval; only then pages and footer links (not added).
- [x] 4.2 Account and company deletion with all their data; data export; tests.
  Done 2026-11-12: storage works on the schema, not a hand list: every table with `company_id` (deleted children
  first by the foreign keys), the company's skills and its blobs (`blobstore.DeleteCompany`). `DELETE /company`
  (signed-in owner or operator, `{"confirm": "<company id>"}`; never Core's own or the default company),
  `GET /company/export` (zip: `manifest.json`, `tables/<table>.json`, `skills.json`, `blobs/<sha256>`; bytea
  columns and token/code hashes left out), `GET /me/export` (`account.json` and the account's companies),
  `DELETE /me` (`{"confirm": "<email>"}`: the account's companies, memberships elsewhere, identities, sessions,
  subscription and billing events; refused with `subscription_active` while a paid subscription still renews).
  Web: Settings → General → Company data (owners), Settings → Account (export, delete; typed confirmation).
  Tests: `internal/storage/data_rights_test.go`, `internal/core/data_rights_test.go`, blobstore.
- [x] 4.3 Support contact on the landing and in the app.
  Done 2026-11-12: `FACTORY_SUPPORT_EMAIL` (unset: nothing shown); `/auth/providers` and `/me` carry it; landing
  footer, company menu "Contact support", Settings → Account. No address is invented.

## 5. Email

- [ ] 5.1 Owner: mail provider; invitations and security notices by email.
  **Blocker:** the owner picks the provider.

## 6. Billing

- [x] 6.1 `saas-billing` done.
  Its tasks 1.1–1.5 are ticked on main (its own blockers: the Paddle sandbox account and the Terms text, the owner's).
- [ ] 6.2 `hosted-connectors` (cloud connectors and Payroll) done, or the launch goes without cloud connectors.
  Another change.
