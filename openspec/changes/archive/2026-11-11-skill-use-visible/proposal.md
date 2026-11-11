Status: implemented 2026-11-11, asked for by the owner ("why does every skill say Every agent").

## Why

The web Skills page labelled each company-wide installed skill "Every agent". The label meant the skill's scope (who
may be given it), but it read as "every agent has it", so the owner concluded that all agents got all skills. In fact
each agent gets only its `spec.skills.include` (tvorog: the CEO has release-readiness and secrets).

## What Changes

- The scope badge says availability: "Any agent may use", "Team NAME only", "Agent NAME only".
- Each installed skill lists the agents that include it ("Used by ceo, developer") or "Not given to any agent yet".

## Impact

- `web/src/lib/api/types-skills.ts`, `web/src/app/c/[company]/skills/page.tsx`; `docs/skills.md`. No API change.
