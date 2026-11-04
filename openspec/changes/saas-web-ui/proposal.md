Status: in progress (owner 2026-11-04: the web is redone for SaaS — landing, sign-in, the product after sign-in with a
company setup wizard, a company selector, everything scoped to the company, settings, an easy download of the node
connector; the UI talks to Core directly). Design decisions D1–D9 are proposals until the owner confirms the open
questions at the end of the design.

## Why

Today's web (`web/`, Next.js) grew as the owner's console: it needs a Node.js server in front of Core (rewrites to
`/api` and `/auth`), it starts inside a company with no landing and no onboarding, the setup and the node pages were
bolted on, and connecting a machine still means a terminal and the owner CLI (`node-install`). A SaaS customer needs
to arrive on a landing page, sign in, get a company that works, set it up step by step or later, and connect a
machine in a minute.

## What Changes

- **Core is the one public address.** Next.js stays (owner): Core serves `/api` and `/auth` and proxies the pages
  to the Next.js server on the private network, so the browser talks to Core directly (D1).
- **Three areas.** Public: landing `/`, sign-in `/signin`, `/connect/<id>` (approve a connector after sign-in).
  Onboarding: `/welcome` after the first sign-in. The product: `/c/<company>/…` — Home (the dashboard), work
  (tasks, inbox, chat, activity, costs) and Settings.
- **Company as tenant everywhere.** A company selector in the top bar (switch, create one when allowed), every page
  and request scoped to the company in the address; the last company is remembered.
- **Onboarding wizard, skippable.** The sign-up company (today `solo`) works as is. The wizard: name the company →
  pick a team preset → connect a machine → connect a project repository → secrets the team needs → the first task.
  Skipping keeps the default company; Home shows "Finish setup" with what is still missing (`GET /setup`).
- **Node connector.** User-facing name for the node. Settings → Connectors: the install command for this Core with
  the platform detected, manual downloads, live list with each connector's runtimes (Claude, Codex) and what they
  miss; a pending sign-in request appears there and on `/connect/<id>` to approve.
- **Settings.** Company (name, members and roles), Teams & agents, Skills, Secrets, Connectors, Projects,
  Integrations (Telegram), Access tokens.

## Impact

- `web/` (new public pages, onboarding, shell, settings, connectors); `cmd/factory/web.go` proxies the pages;
  `web/Dockerfile`, `deploy/dex/Dockerfile`, `docs/deploy-railway.md`; sign-in redirect address is Core's own
  (`FACTORY_PUBLIC_URL` = Core's URL).
- New capability `saas-web-ui`. `owner-web` keeps its API requirements.
- Docs: `docs/owner-ui.md` (web part), `docs/development.md` (web dev), `docs/node-install.md` (connector pages).
- Not here: billing and plans, a marketing site separate from the product, email invitations beyond today's
  memberships, the desktop connector app.
