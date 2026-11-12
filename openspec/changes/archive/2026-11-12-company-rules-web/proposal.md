Status: implemented 2026-11-12, asked for by the owner (the company rules every agent reads had no place in the web).

## Why

Company rules (`GET|PUT /settings/company`, up to 8 KB) go into every agent's brief in every run. On 2026-11-12 the
tvorog prompts were rewritten so company-wide rules live there once instead of in 12 prompts, but the web had no page
for them: the owner could see and change them only with `factory rules company`.

## What Changes

- Settings gets a **Company rules** tab: the rules as text, a size counter against the 8 KB limit, Save for owners and
  admins, read-only for members, and a note on what belongs where (a role's work in its prompt, a project's facts in
  the project's rules).

## Impact

- `web/src/components/app/settings/company-rules.tsx`, `web/src/app/c/[company]/settings/page.tsx`;
  `docs/owner-ui.md`. No API change.
