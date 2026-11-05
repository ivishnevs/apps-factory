Status: checklist before the public SaaS release (raised 2026-11-05). Items are done one by one; items marked
"owner" need the owner's accounts, money or legal decisions.

## Why

The Railway sandbox runs the whole product, but a public release needs a fresh production setup, operations that
survive incidents and the legal pages and data rights people expect. This change keeps them in one place so none is
forgotten before launch.

## What Changes

- **Production environment (owner + me):** a separate Railway environment or project with fresh secrets
  (`FACTORY_TOKEN`, `FACTORY_SECRETS_KEY` backed up outside Railway, OIDC client secret), its own GitHub (and Google)
  OAuth apps, a custom domain for Core and Dex, Dex in Postgres (done in the sandbox), `FACTORY_MULTI_COMPANY` per
  pricing.
- **Operations:** daily Postgres backups with a tested restore; the blob volume backed up; uptime checks on `/health`
  and the sign-in; error alerts from Core's logs; rate limits on the public routes (`/auth/*`, `/node-connects`,
  `/install.sh`, `/downloads/*`, invitation preview); a status page or status notes; an incident runbook.
- **Security:** a security review of the public surface (sessions, CSRF, invitations, connector sign-in, proxy
  rules), dependency updates, secrets rotation procedure.
- **Legal and data:** Terms of Service, Privacy Policy, cookie notice (only necessary cookies today), a data
  processing note (what Core stores: tasks, chats, code metadata, secrets encrypted), account and company deletion
  with all their data, data export, a support contact.
- **Email (owner picks a provider):** invitations by email, sign-in and security notices.

## Impact

Deploy docs, Core (rate limits, deletion and export routes), web (legal pages, footer, deletion in Settings), ops
outside the repository (backups, monitoring).
