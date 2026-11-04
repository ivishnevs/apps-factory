## 0. Decisions

- [x] 0.1 Decisions: Next.js stays with Core as the one public address (owner); defaults for the other questions
  recorded in the design.

## 1. Core as the one address

- [x] 1.1 Core proxies the page paths to Next.js (`FACTORY_WEB_UPSTREAM`), `TestWebPagesThroughCore`.
- [ ] 1.2 Railway: Core, web, Dex (GitHub sign-in), Postgres per `docs/deploy-railway.md`; sign-in works on the
  public address.
- [ ] 1.3 Local development: `FACTORY_WEB_UPSTREAM=http://127.0.0.1:3000` on Core (or `next dev` with its rewrites);
  docs.

## 2. Product shell

- [x] 2.1 Top bar with the company selector (switch, new company), user menu; navigation per D6; phone layout. Done: sidebar groups work and "Manage"; the company menu switches between every company of the person and is on top on phones; Costs only for owners and admins. The Companies page is gone (the operator keeps `factory companies`); "New company" is in the company menu for signed-in people when `FACTORY_MULTI_COMPANY=1` (on in Railway) and opens the new company's wizard; Settings holds only this company and the account.
- [x] 2.2 Home dashboard with the "Finish setup" card. Done: Home shows "Finish setting up" (`SetupBanner`) while `/setup` is not ready.
- [ ] 2.3 Settings section (Company and members, Teams & agents, Skills, Secrets, Connectors, Projects,
  Integrations, Access tokens), role-aware.

## 3. Onboarding and connectors

- [x] 3.1 `/welcome` wizard (D7) and the "seen" flag on the user (Core). Done: `/welcome` sends a person to `/c/<company>/setup?welcome=1` once (seen flag in the browser for now, a Core flag later); the setup page is the wizard: company name, team preset, connector, project, secrets, first task; Skip setup; sign-in returns to `/welcome` by default.
- [x] 3.2 Connectors page and onboarding step (D8): install command, downloads, live status with runtimes. Done: `InstallConnector` (command, code, downloads, needs) in Add a connector, on the empty Connectors page and in the wizard.
- [x] 3.3 `/connect/<id>` approval page; pending requests per the owner's answer. Done: `/connect/<id>`, checked live: approved in the browser, the terminal finished on its own.

## 4. Landing

- [x] 4.1 Landing page (D5) with sign-in; signed-in visitors go to their company. Done: `/` is the landing; signed-in visitors get "Open App Factory".

## 5. Checks and docs

- [ ] 5.1 Browser checks of each phase (desktop and phone), `make check`, web lint and build; docs
  (`owner-ui.md`, `development.md`, `node-install.md`).
