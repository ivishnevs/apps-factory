Status: planned (owner request 2026-11-03: SaaS users get Factory's default skills without setup and add their own
skills and registries from git repositories; decisions D1–D8 confirmed the same day, see design.md). Not started.

## Why

Skills are managed by the operator through files today:

- A company's registry is its catalog's `skills.yaml` with paths to `SKILL.md` on the Core host's disk
  (`docs/skills.md`); skill contents are operator-only (`company-self-service` D5), so a client cannot add a skill.
- Core never sends skill contents to nodes: a node needs the company's catalog and the skill files at the same
  relative places under `skills_root` and refuses a run whose copy differs from the pinned hash
  (`internal/worker/skills.go`). A SaaS client's node has neither our default skills nor their own.
- Team templates point at the repository's `skills/` (`../../skills/...`), which only exists on our hosts.

## What Changes

- **Core owns skill contents:** every skill version is a content-addressed blob in Core (the existing
  `internal/blobstore`, key = `skills.Hash`); Core is the single source of truth, independent of nodes. This
  reverses `company-self-service` D5 for skill contents.
- **Skill sources per company:** a git repository (public, or private with a credential from the secret store)
  holding one skill or a collection, plus the built-in Factory source every company has.
- **Deterministic indexer (no LLM):** fetch a ref, discover skills by the Agent Skills format and common
  repository layouts (`.claude-plugin/marketplace.json`, `skills/*/SKILL.md`, `.claude/skills/`,
  `.agents/skills/`, a root `SKILL.md`), validate, hash, store; per-skill errors reported.
- **Installed skills in the catalog:** `skills.yaml` lists installed skills by source and pinned hash, so catalog
  versions and rollbacks cover skills; updates are explicit (pinned) or automatic on sync (`track`).
- **Skills page in the web UI:** sources (add once by link, sync, errors), library (search, read), installed
  (versions, updates). The agent editor only picks installed skills (Factory and the company's own); there is no
  import in the agent form.
- **Delivery to nodes by hash:** a node downloads a run's skills from Core into a content-addressed store
  `~/.app-factory/skills/store/sha256-…`, verifies them, and each run gets a view with only its agent's skills.
- Optional per-agent scope of an installed skill (`scope: agent:<name>`).

## Out of scope

A public marketplace UI across companies (later: a curated list of sources in the same format), editing skill
contents in the UI, webhooks from git hosts (polling and manual sync first), executing or sandboxing skill
scripts beyond today's runtime policies.

## Impact

`internal/skills` (indexer, registry by source/hash), `internal/storage` (sources, index), `internal/blobstore`
(skill blobs), `internal/core` (sources/sync/library API, blob download for runs, hash pins), `internal/worker`
and `internal/node` (store, download, per-run view, GC), `internal/driver/claude` and `codex` (install from the
view), `configs/templates/*/skills.yaml`, `internal/web` + `web/` (Skills page, picker), docs `skills.md`,
`catalog.md`, `development.md`.
