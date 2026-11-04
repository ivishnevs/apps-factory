Status: planned (owner request 2026-11-03: users set up their teams and agents in the web UI from presets or from
scratch, and everything is stored in Core; decisions D1–D5 confirmed the same day, see design.md). Not started.
Builds on `company-teams` (teams in the catalog) and `user-accounts` (the company created at sign-up).

## Why

A client must start working without effort and still be able to shape each team:

- Starter teams exist only as directories (`configs/templates/{solo,product-team,full-team}`) with role prompts in
  the repository's shared `prompts/roles/`; a company is created from one by the operator (`company-self-service`
  4.2, 6.1).
- There are no agent presets: a client cannot add "an iOS developer with our prompt and skills" to a team.
- Agents are edited only as YAML documents (Team page); there is no form.
- Shared prompts are read from the Core host's disk underneath a stored catalog (`company-self-service` D1), so a
  team created from a template still depends on our files.
- Nothing tells a new user what is still missing before agents can run: secrets the agents need, a node.

## What Changes

- **Presets as product content:** agent presets (agent definition, prompt, Factory skills, runtime defaults,
  secrets it needs) and team presets (agent presets with reporting lines, team document, optional profile and
  policies).
- **Everything copied into the company's catalog:** a team preset creates a **team** in the existing company; an
  agent preset adds an agent to a chosen team. Documents and prompts are copied into a new catalog version; the
  agent records its preset and version for update hints. No live link and no disk dependency afterwards.
- **Wizard (through Core):** first visit: the company exists → create a team from a preset or empty → agents from
  presets or custom → missing secrets (set now or later, never blocking) → connect a node (only when the company
  has none) → first project for the team. Later: "New team" and "New project → choose team".
- **Structured API** over the catalog (`/catalog/teams`, `/catalog/agents`, `/presets/...`) that writes the same
  documents and saves through the existing catalog save (validation, versions, rollback).

## Out of scope

Preset update merging (only a hint and a diff), a public preset marketplace, editing policies in forms (the YAML
editor stays), the team model itself (`company-teams`).

## Impact

New `presets/` in the repository (replaces `configs/templates/`), `internal/config` (preset loading, copy into
documents), `internal/core` (preset, team and agent endpoints, setup status), `internal/web` + `web/` (wizard, team
and agent forms, presets gallery), docs `catalog.md`, `agent-configuration.md`, `owner-ui.md`.
