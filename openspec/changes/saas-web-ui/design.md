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

### D1. A static SPA served by Core (recommended)
Options weighed:
1. **Static build served by Core, same origin** — one container, no CORS, the session cookie just works, the UI is
   always the version of its Core (no API skew), one Railway service as today. Cost: Core serves files (trivial) and a
   web change ships with a Core image.
2. **Static build on a CDN / static host (Cloudflare Pages, S3) + Core as API** — faster landing worldwide and
   independent web deploys, but CORS, cross-origin cookies (`SameSite`, a shared parent domain), and version skew
   between UI and API to manage.
3. **Keep a UI server (Next.js SSR)** — the extra hop the owner wants gone; SSR is not needed: everything behind
   sign-in is client-side and the landing is static.

Choice: 1 now. The build takes its API base from the page's origin, so 2 stays a deployment option later
(`VITE_API_BASE` plus CORS on Core) without code changes in pages.

### D2. Vite + React instead of Next.js (owner to confirm)
Next.js can build static files (`output: "export"`), but only for routes it can list at build time: dynamic
segments like `/c/[company]/tasks/[id]` need `generateStaticParams`, and company names and task ids are not known
then. Workarounds exist — ids in the query string (`/c/tasks?company=acme&id=…`), or one catch-all page whose own
code routes by the path, served by Core for every `/c/*` — but both give up Next's file routing, which is what
Next would be kept for. Keeping Next as a server (`next start`) works too, at the cost of a second service and the
proxy hop. Vite + React with a client router (TanStack Router, typed params, file
routes like today's) builds plain static files. Vite + React with a client router (TanStack Router, typed params, file
routes like today's) builds plain static files. Ported as is: components (`components/ui`, `components/app`), React
Query hooks, the API client, Tailwind, `next-themes` (works outside Next). Replaced: `next/link` and
`next/navigation` (the existing `Link`/`useRouter` wrappers keep their API), `layout.tsx` files become route layouts.

### D3. Routes and how Core serves them
- SPA paths: `/`, `/signin`, `/welcome`, `/connect/*`, `/c/*`; static assets under `/assets/*` (hashed names,
  cached for a year); `index.html` never cached.
- Core keeps its machine routes (`/nodes/…`, `/runs/…`, `/install.sh`, `/downloads/…`, owner API) — the SPA uses
  none of those prefixes. `GET` of an SPA path answers `index.html`; everything else goes to Core's handlers as now.
- Files come from `FACTORY_WEB_DIR` (default `<root>/web/dist`), built into the image by a `web` Dockerfile stage.
  `FACTORY_PUBLIC_URL` becomes Core's own public URL (the sign-in callback and the connect page).

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

## Open questions for the owner

1. Landing: inside the product build (D5) or a separate marketing site from the start? Product name and domain?
2. Navigation: is "Home / Tasks / Inbox / Chat / Activity / Costs + Settings" right; projects first (a project
   switcher under the company) or teams first?
3. Do members (not owners/admins) see costs?
4. "Connector" as the user-facing name for the node — or "runner", "machine"?
5. Pending connectors on the Connectors page: only through the link from the terminal (simplest, safest), or also
   listed by the requesting address?
6. Onboarding team step: replace the sign-up `solo` team with the chosen preset, or add next to it?
7. Visual direction: keep today's neutral shadcn look for MVP, or a branded design pass first?
