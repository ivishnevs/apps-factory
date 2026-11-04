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

- [ ] 2.1 Top bar with the company selector (switch, new company), user menu; navigation per D6; phone layout.
- [ ] 2.2 Home dashboard with the "Finish setup" card.
- [ ] 2.3 Settings section (Company and members, Teams & agents, Skills, Secrets, Connectors, Projects,
  Integrations, Access tokens), role-aware.

## 3. Onboarding and connectors

- [ ] 3.1 `/welcome` wizard (D7) and the "seen" flag on the user (Core).
- [ ] 3.2 Connectors page and onboarding step (D8): install command, downloads, live status with runtimes.
- [ ] 3.3 `/connect/<id>` approval page; pending requests per the owner's answer.

## 4. Landing

- [ ] 4.1 Landing page (D5) with sign-in; signed-in visitors go to their company.

## 5. Checks and docs

- [ ] 5.1 Browser checks of each phase (desktop and phone), `make check`, web lint and build; docs
  (`owner-ui.md`, `development.md`, `node-install.md`).
