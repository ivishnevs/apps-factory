Status: planned (owner request 2026-11-03: SaaS users get Factory's default skills without setup and add their own
skills and registries from git repositories once per company; decisions D1–D9 confirmed the same day, see
design.md). Not started.

## Why

Skills are managed by the operator through files today:

- A company's registry is its catalog's `skills.yaml` with paths to `SKILL.md` on the Core host's disk
  (`docs/skills.md`); skill contents are operator-only (`company-self-service` D5), so a client cannot add a skill.
- Core never sends skill contents to nodes: a node needs the company's catalog and the skill files at the same
  relative places under `skills_root` and refuses a run whose copy differs from the pinned hash
  (`internal/worker/skills.go`). A client's node (their own machine) has neither our default skills nor theirs.
- Team templates point at the repository's `skills/` (`../../skills/...`), which only exists on our hosts.

## What Changes

- **Core owns skill contents:** every skill version is a content-addressed blob in Core (the existing
  `internal/blobstore`, key = `skills.Hash`); Core is the single source of truth, independent of nodes. This
  reverses `company-self-service` D5 for skill contents.
- **Skill sources per company:** the built-in Factory source every company has; git repositories (one skill or a
  collection); uploaded archives. Public repositories are fetched by Core; private ones by the company's node with
  the git access of the user's machine, which uploads the tree for Core to index. Core keeps no git credentials.
- **Deterministic indexer (no LLM):** discover skills by the Agent Skills format and common repository layouts
  (`.claude-plugin/marketplace.json`, `skills/*/SKILL.md`, `.claude/skills/`, `.agents/skills/`, a root
  `SKILL.md`), validate, hash, store; per-skill errors reported.
- **Installed skills in the catalog:** `skills.yaml` lists installed skills by source and pinned hash, so catalog
  versions and rollbacks cover skills; updates are explicit (pinned) or automatic on sync (`track`). A skill may be
  scoped to a team or an agent.
- **Skills page in the web UI:** sources (added once by link or upload, sync, errors), library (search, read),
  installed (versions, updates). The agent editor only picks installed skills available to its team.
- **Delivery to nodes by hash:** a node downloads a run's skills from Core into a content-addressed store
  `~/.app-factory/skills/store/sha256-…`, verifies them, and each run gets a view with only its agent's skills.

Depends on `node-identity` (signed node requests for blob downloads and tree uploads) and `company-teams`
(`scope: team:<name>`).

## Out of scope

A public marketplace UI across companies (later: a curated list of sources in the same format), editing skill
contents in the UI, webhooks from git hosts (manual and periodic sync first), Core-held git credentials (deploy
keys, GitHub App: later, as attributes of a source), executing or sandboxing skill scripts beyond today's runtime
policies.

## Impact

`internal/skills` (indexer, registry by source/hash), `internal/storage` (sources, index, sync jobs),
`internal/blobstore` (skill blobs), `internal/core` (sources/sync/library API, node sync jobs, blob download for
runs, hash pins), `internal/worker` and `internal/node` (store, download, per-run view, GC, private source fetch;
`skills_root` removed), `internal/driver/claude` and `codex` (install from the view),
`configs/templates/*/skills.yaml`, `internal/web` + `web/` (Skills page, picker), docs `skills.md`, `catalog.md`,
`development.md`.
