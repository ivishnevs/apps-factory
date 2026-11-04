## Context

- Core already serves the web API in process (`/api/*`, `web.HandlerTransport`, saas-web) and the sign-in (`/auth/*`,
  OIDC through Dex, session cookie). The Next.js app only proxies to it; every page is a client component (22 pages,
  ~110 files: shadcn/Radix, React Query, `lib/api/client.ts`).
- First sign-in creates the person's company from the `solo` template (user-accounts D1, `DefaultSignupTemplate`).
- Company scoping: `X-Factory-Company` header plus the `factory_company` cookie; roles owner/admin/member.
- `GET /setup` reports what keeps work from running; `node-install` added `GET /node-install`, connection requests
  (`/node-connects/…`) and Core-hosted packages.

## Goals / Non-Goals

Goals: one deployable (Core) serves landing, sign-in and the product on one origin; a new customer reaches a working
company and a connected machine without a terminal beyond one pasted command; every product page is the company's.
Non-goals: server-side rendering, billing, a CMS-driven marketing site, native apps.

## Decisions

### D1. Next.js stays; Core is the one public address (owner 2026-11-04)
The owner chose to keep Next.js for now (less to change) with the browser talking to Core directly. Core serves
`/api` and `/auth` itself and proxies the page paths (`/`, `/signin`, `/welcome`, `/connect/*`, `/c/*`, `/_next/*`)
to the Next.js server on the private network (`FACTORY_WEB_UPSTREAM`, `cmd/factory/web.go`). The session cookie is
first-party, there is no CORS, and API calls and event streams never pass through Next. Weighed and set aside: a
static build (Next's static export cannot serve `/c/[company]/…` without workarounds; Vite would mean a port), a
web domain separate from Core's (Railway's `*.up.railway.app` is a public suffix, so the cookie would be
third-party). Deploy: `docs/deploy-railway.md` (Core, web, Dex, Postgres).

### D2. Where pages live
Next's app router as today; new route groups: `(public)` for `/`, `/signin`, `/connect/[id]`; `/welcome`; the
product under `/c/[company]`. Pages stay client components over the existing API client.

### D3. Core's routes and the pages never collide
The page paths above are the only ones Core proxies (GET and HEAD); a test checks that `/api`, `/auth`, node and
owner routes stay Core's (`TestWebPagesThroughCore`). `FACTORY_PUBLIC_URL` is Core's own address.

### D4. Sign-in and local mode
Same origin: the SPA calls `/auth/login?return=…`; Core's session cookie covers `/api`. A 401 sends the person to
`/signin?return=…` (as today). `FACTORY_WEB_LOCAL=1` (loopback Core, no sign-in) stays for development and
self-hosting on one machine. Dev: `vite dev` proxies `/api` and `/auth` to `FACTORY_URL`.

### D5. Landing
A static, fast page in the same build (own entry, little JS): what App Factory is, how it works (company → team of
agents → your machine runs them → you approve), sign-in buttons (Google, GitHub). A signed-in visitor sees "Open
App Factory" (to the last company). Content is a placeholder to be written with the owner; it can move to a
separate marketing site later without touching the app.

### D6. Shell and navigation
- Top bar: company selector (name, role; switch; "New company" when `multi_company`), search/palette later, user
  menu (profile, theme, sign out).
- Left navigation (bottom bar on phones): **Home**, **Tasks**, **Inbox** (badge), **Chat**, **Activity**,
  **Costs** (owners/admins by default, see questions); **Settings** at the bottom.
- Home (the dashboard): "Finish setup" card while `/setup` is not ready; what the agents do now; what waits for
  the person (approvals, questions); connectors' health; today's cost.
- Settings: Company (name, members and roles), Teams & agents (team builder pages), Skills, Secrets, Connectors,
  Projects, Integrations, Access tokens. Members see only what their role may change (`requiredRole`).

### D7. Onboarding
After the first sign-in the person lands on `/welcome` for the sign-up company: 1 name the company (renames the
created one); 2 team preset (gallery from `GET /presets/teams`; replaces the `solo` team, or keeps it); 3 connect a
machine (D8, waits live for the connector and shows its runtimes); 4 project repository (create or link, today's
project creation); 5 secrets the team needs; 6 first task to the lead (or open chat). Every step can be skipped;
"Skip setup" goes to Home with the default company. Progress is not stored separately: the steps read `/setup`,
`/catalog/teams`, `/nodes` and the projects, so a later visit shows what is still missing and `/welcome` (or the
Home card) continues it. Whether a person has seen the wizard is a flag on the user (so it opens once on its own).

### D8. Connectors
User-facing "connector" for the node (`factory-node` keeps its name in commands and docs for now). Settings →
Connectors and onboarding step 3 show:
- the one-line command from `GET /node-install`, the platform detected from the browser, copy button; "with a
  code" for machines without a browser (issues an enrollment code; the command gets `--code`);
- manual downloads (`/downloads/node/…`, `SHA256SUMS`) and what the machine needs (Node.js 22+, git; Claude and
  Codex sign-in);
- live: pending sign-in requests (needs a list route `GET /node-connects?status=pending` for the company's admins —
  no company yet, so: requests created in the last 15 minutes from the same address as the browser, or only via the
  link; see questions), connected connectors with their runtimes and doctor-level problems.
`/connect/<id>`: after sign-in, the company is chosen (selector preset to the last one; only owner/admin
companies), the request shows name, system, address, time, the code to compare with the terminal and the
fingerprint; Connect / Cancel. The terminal continues on its own.

### D9. Visual direction
Kept from today: shadcn/Radix, Tailwind, light and dark, responsive. A design pass (with the designer agent or the
owner) settles brand, type and density before the port is styled; the port first reaches parity.

## Risks / Trade-offs

- The port touches every page → done in phases (shell and routing first with pages ported 1:1, then onboarding,
  connectors, landing), each phase shippable.
- Web changes now ship with Core → acceptable (one product, one version); CDN hosting later if landing speed matters.
- SPA routes must never collide with Core routes → a test lists Core's route prefixes against the SPA prefixes.

## Defaults taken (owner 2026-11-04: "record the decisions and rework the web in parallel")

The open questions got these defaults so work goes on; each can be changed later without rework of the rest:
1. Landing inside the product (D5), placeholder copy; product name "App Factory".
2. Navigation as in D6; teams and projects live under Settings, the company is the top level.
3. Costs: owners and admins see them; members do not (the menu item hides, Core's role rules stay as they are).
4. "Connector" in the UI for the node; commands keep `factory-node`.
5. Pending connectors only through the link the terminal prints (`/connect/<id>`), no list by address.
6. Onboarding team step adds the chosen team; when the company still has only the untouched sign-up `solo` team, the
   step offers to replace it.
7. Visual direction: today's neutral shadcn look for MVP; a design pass later.

## Questions the defaults answered (for the record)

1. Landing: inside the product build (D5) or a separate marketing site from the start? Product name and domain?
2. Navigation: is "Home / Tasks / Inbox / Chat / Activity / Costs + Settings" right; projects first (a project
   switcher under the company) or teams first?
3. Do members (not owners/admins) see costs?
4. "Connector" as the user-facing name for the node — or "runner", "machine"?
5. Pending connectors on the Connectors page: only through the link from the terminal (simplest, safest), or also
   listed by the requesting address?
6. Onboarding team step: replace the sign-up `solo` team with the chosen preset, or add next to it?
7. Visual direction: keep today's neutral shadcn look for MVP, or a branded design pass first?

## Proposals that need the owner's decision

- **P1. Inviting people (decision needed) — moved to the change `team-invitations`.** Memberships may only join a user and a company of the same account
  (composite keys of `user-accounts`), and every first sign-in creates its own account, so a person cannot be
  added to someone else's company today. Proposal: memberships across accounts (drop the account from the
  membership keys; the company's account stays the billing owner), invitation links from Settings → Members
  (`POST /invitations {role}` → a single-use link valid 7 days; `/invite/<token>` signs in and joins), and email
  delivery later. Until decided, Members lists and manages the people already there and says invitations are not
  available yet.
- **P2. Telegram per company (decision needed) — moved to the change `telegram-per-company`.** One bot per Core (`FACTORY_TELEGRAM_TOKEN`) serves the default
  company only. Proposal: one shared bot; Settings → Integrations issues a bind code per company and person
  (`/start <code>`), notifications go per membership. Until decided, the web shows no Telegram settings.
- **P3. "Seen the wizard" in Core — done.** `users.welcomed_at` (migration 0018), `POST /me/welcomed` when the wizard
  is finished or skipped; a token keeps the browser flag.
