Status: idea recorded 2026-11-05 by the owner; to be designed. Nothing implemented in the product (done by hand for
the game team, catalog version 42).

## Why

A team lead learns about its teammates through `factory_agent_list`, which returns only each agent's name, role and
`capabilities` (a list of strings in the agent's config); the agent's `description` is not included. No preset and
no created agent sets `capabilities`, so a lead sees "artist-3d, role artist" and must guess what the agent does,
what it needs as input and who reviews it. When a 3D artist was added to the game team, the lead had no way to know
that 3D work needs a 2D concept first. Without this the quality of planning drops: wrong assignees, missing
dependencies, work started without its inputs.

## What Changes (proposed)

- **Every default agent preset has capabilities**: what it does, what it needs as input (and from whom), what it
  produces, who reviews it — a few plain lines (as written for the game team on 2026-11-05).
- **Creating or editing an agent recommends them**: the agent editor and the team wizard show a capabilities field
  with a template and examples; saving an agent without capabilities warns (or the catalog loader reports it) —
  to decide whether it is required.
- **Leads get the roster without asking**: the team's agents with role, description and capabilities are part of
  the lead's brief (and of every agent's brief for its own team), not only behind `factory_agent_list`; the tool
  also returns the description.
- **Lead instructions** (built-in for the manager role) say to follow the capabilities' input chains when splitting
  work and to link tasks with dependencies.

## Impact

Presets (`presets/agents`), agent editor and team wizard (web), catalog validation (warning), run briefs (team
roster for leads), `factory_agent_list`. Related: `default-agent-library`, `team-page-clarity`,
`lead-brainstorm-mode`, `review-coverage`.
