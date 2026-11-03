Planned (owner 2026-11-03: D1–D8 confirmed, Core owns skill contents). Not started. Order: blobs and delivery
by hash with the built-in source (SaaS gets default skills on any node), then git sources, indexer and UI, then
updates and private repositories.

## 1. Decide
- [x] 1.1 Owner confirms Core owns skill contents, reversing company-self-service D5 for skills (D2).
- [x] 1.2 Owner confirms sources per company managed once on a Skills page, picker only in the agent form (D5).
- [x] 1.3 Owner confirms the node store `~/.app-factory/skills` with a per-run view (D7).

## 2. Specs and docs
- [x] 2.1 Delta spec `skill-registry`; `openspec validate --strict`.
- [ ] 2.2 Docs: `skills.md` (formats, sources, installed skills, delivery), `catalog.md` (skills.yaml shape,
  D5 change), `development.md` (node store, GC, `skills_root` fallback).

## 3. Blobs and delivery
- [ ] 3.1 Skill blobs in `internal/blobstore` keyed by `skills.Hash` (tar of the directory, deterministic);
  reserved scope `factory` for built-in skills.
- [ ] 3.2 Built-in source: the release's default skills indexed at Core start; templates' `skills.yaml` use
  `source: factory` instead of `../../skills/...`.
- [ ] 3.3 Loader: installed skills by source and hash (`path:` kept for disk catalogs); Core reads served from
  blobs; `skill_hashes` pinned from the installed hashes.
- [ ] 3.4 `GET /skills/blobs/{hash}` for run credentials, only hashes pinned on the run.
- [ ] 3.5 Node store `~/.app-factory/skills/store/sha256-*` (download to `tmp/`, verify, atomic rename,
  read-only), per-run view, drivers install from the view; GC of unused entries; `skills_root` fallback.

## 4. Sources and indexer
- [ ] 4.1 Migration: `skill_sources`, `skill_index`.
- [ ] 4.2 Fetch: allowed schemes, public addresses only, shallow, no submodules/LFS/hooks, limits; ref → commit.
- [ ] 4.3 Discovery by layout (D3), validation, hashing, per-skill errors; fixtures for each layout.
- [ ] 4.4 API and CLI: sources (add, sync, remove, attach to another company of the user), library (search,
  read file), install/uninstall (catalog version), `factory skills sources|sync|library|install`.

## 5. Updates, private repositories, scope
- [ ] 5.1 `pinned` updates with file diff; `track` installs on sync (author `sync`); periodic sync.
- [ ] 5.2 Private repositories with a credential from `core-secret-store`.
- [ ] 5.3 `scope: agent:<name>` validation and discovery.

## 6. Web UI
- [ ] 6.1 Skills page: Sources, Library (render SKILL.md, file tree), Installed (versions, updates).
- [ ] 6.2 Agent editor skill picker: installed Factory and company skills, discovery categories, link to Skills.

## 7. Verification
- [ ] 7.1 `make check` green plus NATS tests with `-p 1`; tests: deterministic index of the same commit, invalid
  skill does not block others, SSRF refusal, rollback restores content, foreign hash 404, node download and
  verification, two agents on one node see only their skills.
