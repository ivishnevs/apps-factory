Status: in progress (owner request 2026-11-01: clients must be able to set up their own company; decisions D1–D6
confirmed the same day, see design.md). Nothing is implemented yet.

## Why

Client companies will be configured by the clients themselves: their own roles (maybe no developer at all), their
own prompts, policies and review rules. Everything needed exists as configuration, but only as files on the Core
host:

- A company is a directory `configs/<company>/` (`agents/`, `profiles/`, `policies/`, `skills.yaml`,
  `company.yaml`, `node-hooks/`, `quota_keys.yaml`) with prompt files under the factory root
  (`docs/development.md`, `docs/agent-configuration.md`).
- The catalog is loaded when Core starts (`cmd/factory/main.go`); there is no API or UI to create a company or
  to create or edit an agent, profile, policy or prompt (only reads such as `factory agents get`/`authorize`).
- One Core process serves one company (`FACTORY_COMPANY`, `docs/architecture.md`).
- Review cannot be configured: exit tools are offered to every agent unfiltered (`internal/core/exits.go`), so any
  agent may close its task with `factory_exit_done`; whether work goes to review is decided by its prompt only.

## What Changes

- **Company provisioning:** create a company (and its first owner) through the API, CLI and web UI instead of a
  directory on the Core host; start it from a starter team template.
- **Catalog in Core storage:** agents, profiles, policies, prompts, the skills registry and company settings are
  stored in Core (versioned, with who/when), validated exactly like `factory config validate`, and applied without
  restarting Core. Runs keep capturing the resolved config snapshot, so a change never alters a running run.
- **Editing API, CLI and web UI:** list, create, edit, delete and roll back agents, profiles, policies and prompts,
  with validation errors at the field and a diff before saving. Agents may propose changes; the existing system
  policy already requires the owner's approval for `factory.config.*`.
- **Team templates:** a few starter companies (e.g. solo builder; product owner + developer + reviewer; product
  owner + designer + developer + QA) the client copies and edits.
- **Review as policy:** a new policy rule type that says, per role or agent, whether review is required before
  `done`, who reviews by default and (later) which paths need whose approval (e.g. requirement changes need the
  product owner). Core enforces it at `factory_exit_done` using the approval it already binds to the pull request.
- No data migration and no backward compatibility (owner decision 2026-11-01: App Factory is not released): the
  file-based catalog may be replaced outright; the repository's own `configs/` can become seed data or templates.

## Out of scope

Billing, sign-up and account management (the SaaS step), a marketplace of templates, editing skills' contents
(only the registry), node setup by clients.

## Impact

`internal/config` (load from storage, validation reused), `internal/storage` (catalog tables with versions),
`internal/core` (catalog API, reload, review policy at exits), `cmd/factory` (company and catalog commands),
`internal/web` + `web/` (company setup and team editor), `internal/tui` (optional), docs
`agent-configuration.md`, `policies.md`, `development.md`, `explicit-exits-contract.md`.
