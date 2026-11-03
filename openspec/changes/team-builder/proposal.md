Status: planned (owner request 2026-11-03: users configure their company, team and agents in the web UI, from
presets or from scratch, and everything is stored in Core; decisions D1–D5 confirmed the same day, see design.md).
Not started.

## Why

A client must start working without effort and still be able to shape the team:

- Starter teams exist only as directories (`configs/templates/{solo,product-team,full-team}`) with role prompts in
  the repository's shared `prompts/roles/`; a company is created from one by the operator (`company-self-service`
  4.2, 6.1).
- There are no agent presets: a client cannot add "an iOS developer with our prompt and skills" to their team.
- Agents are edited only as YAML documents (Team page); there is no form for creating an agent.
- Shared prompts are read from the Core host's disk underneath a stored catalog (`company-self-service` D1), so a
  company created from a template still depends on our files.

## What Changes

- **Presets as product content:** agent presets (agent definition, prompt, Factory skills, runtime defaults) and
  team presets (a set of agent presets with reporting lines, profile, policies, company settings).
- **Everything copied into the company's catalog:** creating from a preset copies all documents, prompts
  included, into a new catalog version; the agent records its preset and version for later update hints. No live
  link and no disk dependency afterwards.
- **Flows in the web UI (through Core):** create a company from a team preset; or create an empty company and
  add agents from presets or fully custom agents; edit an agent in a form (role, prompt, runtime, reporting,
  communication, skills from the installed ones).
- **Structured agent API** over the catalog (`/catalog/agents`, `/presets/...`) that writes the same documents
  and saves through the existing catalog save (validation, versions, rollback).

## Out of scope

Preset update merging (only a hint and a diff), a public preset marketplace, several teams inside one company,
editing policies in forms (the YAML editor stays).

## Impact

New `presets/` in the repository (replaces `configs/templates/`), `internal/config` (preset loading, copy into
documents), `internal/core` (preset and agent endpoints, company creation with a preset), `internal/web` + `web/`
(company wizard, agent form, presets gallery), docs `catalog.md`, `agent-configuration.md`, `owner-ui.md`.
