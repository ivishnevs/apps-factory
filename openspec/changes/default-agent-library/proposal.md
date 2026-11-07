Status: direction decided 2026-11-06 by the owner (see "Decision"); the concrete presets are generated during
implementation. Nothing implemented. The game studio (first real team) is the pilot.

## Why

The built-in library is tiny: 8 agent presets (mostly software roles), 4 team presets and 3 Factory skills
(`openspec`, `task-pull-request`, `mobile-app-testing`). A non-technical user who signs up finds nothing for
marketing, support, content, sales, research or finance, and the agents they do find have almost no skills.
Adding skills today means a git source, a private repository through a node or an archive, which only advanced
users will do. The library must be the product: open the catalog, pick agents from many fields, each already
equipped, and start.

## What Changes (proposed)

- **A default library of 30+ agent presets across fields**, grouped by category with plain titles, a one-line
  "what it does", example tasks and what it needs (a connector, a runtime, secrets such as a store or ads account).
  Starting set (titles, to be refined):
  - Software: product owner, developer, web developer, iOS developer, Android developer, backend developer,
    reviewer, QA, DevOps, designer (UI/UX), technical writer.
  - Games: game designer, gameplay developer, level designer, game artist (2D assets), sound and music, playtester,
    live-ops and store.
  - Growth: marketer, SEO, content writer, copywriter, social media, email campaigns, ASO.
  - Business: researcher (market, competitors), analyst (data, metrics), sales (leads, outreach drafts), support,
    finance assistant (bookkeeping drafts, invoices), legal drafts (never legal advice), recruiter, project manager.
- **Every preset comes with its skills.** The Factory source grows from 3 skills to a curated set (domain skills
  for each preset: e.g. `game-design-doc`, `playtest-report`, `seo-audit`, `content-calendar`, `market-research`,
  `release-checklist`, document skills for docx/xlsx/pdf/pptx where licenses allow). Skills are written by us or
  vendored from permissively licensed sources (Apache-2.0/MIT) with attribution, reviewed and pinned by hash in the
  release; nothing is fetched at runtime.
- **Team presets per field**: game studio, SaaS product, mobile app, marketing team, content studio, research desk,
  support desk, solo founder (a small generalist team).
- **Library UI for non-technical users**: catalog with categories, search, cards, "add to my team" and "create a team
  from this preset" in one step; skills shown as plain capabilities ("writes a game design document"), not as
  repositories. Presets are copied into the company (team-builder D1), so users can edit them afterwards.
- **Sources stay as the advanced path.** Git sources, private repositories through a node and archives remain on the
  Skills page under "Advanced: add your own skills"; nothing in the default flow requires them.
- **Quality bar**: each preset has a prompt, skills, the role and approvals it needs, and an eval task that a sandbox
  run passes before the preset ships; presets that need an external account say so and ask for the secret.
- **Pilot: game studio.** Use the owner's first game team to shape the format (prompts, skills, review rules), then
  scale the format to the other fields.

## Impact

- `presets/agents`, `presets/teams`, `skills/` (many new files, release size), team-builder and skill-registry
  (categories, tags, requirements on presets), web catalog and team wizard.
- License review for every vendored skill; attribution file in the release.
- Related: `saas-release-readiness` (the library is part of the launch), `hosted-connectors` (presets that need no
  connector of one's own).

## Decision (2026-11-06)

The product ships a large, ready-configured catalog of skills and agents, and ready teams assembled from them. A
person who does not want to deal with settings picks a ready team; one who does builds their own team by choosing
agents from the catalog (and adjusting them). Which agents, skills and teams exactly is generated during
implementation (by field, with the game studio as the pilot), not decided up front.

## Implementation (2026-11-07)

- Library: 30 agent presets (software 11, games 7, growth 3, content 3, business 6) and 10 team presets (Game studio,
  Web product, Growth, Content studio, Business operations, plus the five earlier ones), 15 Factory skills (12 new,
  Apache-2.0, each with an eval task).
- Format: `category` and `examples` on presets, validated by the loader; skills carry `license` and `metadata.eval`,
  with an 8 KiB / 64 KiB budget checked by a test; `docs/library.md`.
- Web: Teams & agents → Library (fields, search, Create this team, Add to a team); Skills shows "What agents can do"
  first and sources under "Advanced".
- Pilot: the game studio presets are the owner's running Gnome Arena team (producer, designer, gameplay, network
  and UI developers, 2D artist, QA; designer reviews art, producer reviews design and QA work) generalised; it has
  been running on the owner's project since 2026-11-06, and its fixes (lead review of uncovered roles, art review,
  two concurrent runs for busy roles) are in the presets.
- Decided during implementation: the concrete list above (owner: "generated during implementation"); business
  presets that touch money, law or hiring prepare work for people and never decide.
