# Deploying on Railway

One project, four services. Core is the only public address the browser and the nodes use; it serves `/api`,
`/auth`, the node routes and the downloads itself and proxies the web UI's pages to the Next.js service on the
private network (`FACTORY_WEB_UPSTREAM`), so the session cookie stays first-party and the browser calls Core
directly. Dex is public too: the browser is sent to it to sign in.

| Service | Source | Public | Notes |
|---|---|---|---|
| Postgres | Railway database | no | Core's database |
| `apps-factory` (Core) | repository, `Dockerfile` | yes (`railway domain`) | volume at `/data` |
| `web` | repository, root directory `web` | no | Next.js, `npm run build`, `npm start` |
| `dex` | repository, Dockerfile path `deploy/dex/Dockerfile` | yes, port 5556 | state in its own `dex` database on the same Postgres |

## Core variables

```
FACTORY_DATABASE_URL=${{Postgres.DATABASE_URL}}
FACTORY_DATA_DIR=/data
FACTORY_BLOB_DIR=/data/blobs
RAILWAY_RUN_UID=0                       # the image runs as nonroot; the volume is root's
FACTORY_TOKEN=<random, the operator token>
FACTORY_SECRETS_KEY=<openssl rand -base64 32, backed up outside Railway>
FACTORY_PUBLIC_URL=https://<core domain>
FACTORY_CORE_URL=https://<core domain>
FACTORY_WEB_UPSTREAM=http://${{web.RAILWAY_PRIVATE_DOMAIN}}:${{web.PORT}}
FACTORY_OIDC_ISSUER=https://<dex domain>/dex
FACTORY_OIDC_CLIENT_SECRET=<random, same as Dex's>
FACTORY_OIDC_PROVIDERS=github
FACTORY_MULTI_COMPANY=1                  # people add companies from the company menu
# FACTORY_BILLING=paddle + FACTORY_PADDLE_*: plans and payments, see billing.md (unset: no plan limits)
# FACTORY_SUPPORT_EMAIL=<address>: the support contact on the landing and in the app (unset: none shown)
# FACTORY_COMPANY_STORAGE_GB=<GiB>: stored content per company (unset: 20 with billing on, none otherwise; 0: none)
```

Incidents, the status note (`PUT /status-note` with the operator token), rate limits on the public routes and
people's export and deletion requests: `incident-runbook.md`.

Telegram needs nothing here: each company adds its own bot in Settings → Integrations, and with the https
`FACTORY_PUBLIC_URL` Core takes its updates by webhook (`/telegram/hook/<company>`).

Core listens on `PORT` (Railway sets it). `FACTORY_WEB_LOCAL` stays unset: on a public Core the web needs sign-in.

## web variables

`PORT=3000` (Next.js listens on it; Core's `FACTORY_WEB_UPSTREAM` names it). Node.js 22+.

## dex variables

```
DEX_STORAGE=postgres
DEX_DB_HOST=${{Postgres.PGHOST}}
DEX_DB_PORT=${{Postgres.PGPORT}}
DEX_DB_USER=${{Postgres.PGUSER}}
DEX_DB_PASSWORD=${{Postgres.PGPASSWORD}}
DEX_DB_ADMIN_DATABASE=${{Postgres.PGDATABASE}}   # where the image connects to create the dex database once
DEX_ISSUER=https://<dex domain>/dex
FACTORY_PUBLIC_URL=https://<core domain>         # Dex sends people back to <core>/auth/callback
FACTORY_OIDC_CLIENT_SECRET=<same as Core's>
GITHUB_CLIENT_ID=...
GITHUB_CLIENT_SECRET=...
```

Dex keeps its signing keys and sign-ins in progress in a database `dex` next to Core's (its tables do not mix with
Core's). The image creates it on first start (`deploy/dex/ensure-db.sh`) from the private network, so Postgres needs
no public address; `DEX_DB_CREATE=0` skips that when the database is made by hand. `DEX_STORAGE=memory` runs without
a database (a restart then drops sign-ins in progress and rotates the signing keys).

## GitHub sign-in

Create an OAuth App (GitHub → Settings → Developer settings → OAuth Apps): homepage `https://<core domain>`,
authorization callback URL `https://<dex domain>/dex/callback`. Its client id and secret go to Dex's variables.
Google works the same way (`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, callback `https://<dex domain>/dex/callback`,
add `google` to `FACTORY_OIDC_PROVIDERS`).

Every push to `main` deploys.
