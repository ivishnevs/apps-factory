## 1. Production environment

- [ ] 1.1 Owner: production Railway environment, custom domains, OAuth apps (GitHub, Google); I set the internal
  secrets and the services as in `docs/deploy-railway.md`.
- [x] 1.2 Dex keeps its state in Postgres; the image creates its database (sandbox done 2026-11-05).
- [ ] 1.3 `FACTORY_SECRETS_KEY` backed up outside Railway; rotation procedure written down.

## 2. Operations

- [ ] 2.1 Daily Postgres backups and a restore drill; blob volume backup.
- [ ] 2.2 Uptime checks (`/health`, sign-in) and error alerts.
- [ ] 2.3 Rate limits on public routes (auth, connector sign-in, installer, downloads, invitation preview); tests.
- [ ] 2.4 Incident runbook and a status page or status notes.

## 3. Security

- [ ] 3.1 Security review of the public surface; findings fixed.
- [ ] 3.2 `connector-trust` done (runs confined, local policy, signed releases, Security page, pentest).
- [ ] 3.3 Dependency update pass (Go, web, Claude worker, Dex image).

## 4. Legal and data

- [ ] 4.1 Owner: Terms of Service and Privacy Policy texts (I can draft them for a lawyer); pages and footer links.
- [ ] 4.2 Account and company deletion with all their data; data export; tests.
- [ ] 4.3 Support contact on the landing and in the app.

## 5. Email

- [ ] 5.1 Owner: mail provider; invitations and security notices by email.

## 6. Billing

- [ ] 6.1 `saas-billing` done.
- [ ] 6.2 `hosted-connectors` (cloud connectors and Payroll) done, or the launch goes without cloud connectors.
