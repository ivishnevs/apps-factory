## 0. Decisions

- [ ] 0.1 Owner answers the open questions of the design (landing, navigation, costs for members, connector name,
  pending connectors, onboarding team step, visual direction).

## 1. Static SPA served by Core

- [ ] 1.1 Vite + React + TanStack Router app in `web/`, port the shell, routing, API client and pages 1:1 (parity
  with today's web); remove Next.js.
- [ ] 1.2 Core serves the build (`FACTORY_WEB_DIR`, SPA fallback for `/`, `/signin`, `/welcome`, `/connect/*`,
  `/c/*`, cache headers); Dockerfile `web` stage; `factory web --demo` serves the same build; route-collision test.
- [ ] 1.3 Sign-in on the same origin (`FACTORY_PUBLIC_URL` = Core's URL), local mode, dev proxy; docs.

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
