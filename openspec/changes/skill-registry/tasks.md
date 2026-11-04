Planned (owner 2026-11-03: D1–D9 confirmed, Core owns skill contents). Not started. No migration (not released).
Depends on `node-identity` (signed node requests) and `company-teams` (team scope). Order: blobs and delivery by
hash with the built-in source (default skills on any node), then sources, indexer and UI, then updates.

## 1. Decide
- [x] 1.1 Owner confirms Core owns skill contents, reversing company-self-service D5 for skills (D2).
- [x] 1.2 Owner confirms sources per company managed once on a Skills page, picker only in the agent form (D6).
- [x] 1.3 Owner confirms private repositories fetched by the company's node, no Core-held git credentials (D3).
- [x] 1.4 Owner confirms the node store `~/.app-factory/skills` with a per-run view (D8).

## 2. Specs and docs
- [x] 2.1 Delta specs `skill-registry`, `agent-config`, `company-catalog`; `openspec validate --strict`.
- [ ] 2.2 Docs: `skills.md` (formats, sources, installed skills, scope, delivery), `catalog.md` (skills.yaml shape,
  D5 change), `development.md` (node store, GC, `skills_root` removed).

## 3. Blobs and delivery
- [ ] 3.1 Skill blobs in `internal/blobstore` keyed by `skills.Hash` (tar of the directory, deterministic);
  reserved scope `factory` for built-in skills.
- [ ] 3.2 Built-in source: the release's default skills indexed at Core start; templates and presets use
  `source: factory` instead of `../../skills/...`.
- [ ] 3.3 Loader: installed skills by source and hash; Core reads served from blobs; `skill_hashes` pinned from the
  installed hashes; `path:` entries removed.
- [ ] 3.4 `GET /runs/{run}/skills/{hash}` for signed node requests (`node-identity`), only hashes pinned on the named run
  of that node.
- [ ] 3.5 Node store `~/.app-factory/skills/store/sha256-*` (download to `tmp/`, verify, atomic rename, read-only),
  per-run view, drivers install from the view; GC of unused entries; `skills_root` removed.

## 4. Sources and indexer
- [ ] 4.1 Schema: `skill_sources`, `skill_index`, source sync jobs.
- [ ] 4.2 Public fetch by Core: `https`, public addresses only, shallow, no submodules/LFS/hooks, limits; ref → commit.
- [ ] 4.3 Private fetch by a node: sync job to an online node of the company, fetch with the node's git access, packed
  tree uploaded with a signed request; waiting state without an online node.
- [ ] 4.4 Upload of an archive or folder (owner/admin).
- [ ] 4.5 Discovery by layout (D4), validation, hashing, per-skill errors; fixtures for each layout.
- [ ] 4.6 API and CLI: sources (add, sync, remove), library (search, read file), install/uninstall (catalog version),
  `factory skills sources|sync|library|install`.

## 5. Updates and scope
- [ ] 5.1 `pinned` updates with file diff; `track` installs on sync (author `sync`); periodic sync.
- [ ] 5.2 `scope: team:<name>` and `scope: agent:<name>` validation, discovery and picker filtering.

## 6. Web UI
- [ ] 6.1 Skills page: Sources (link or upload, sync, status), Library (render SKILL.md, file tree), Installed
  (versions, updates, scope).
- [ ] 6.2 Agent editor skill picker: installed skills available to the team, discovery categories, link to Skills.

## 7. Verification
- [ ] 7.1 `make check` green plus NATS tests with `-p 1`; tests: deterministic index of the same tree, invalid skill
  does not block others, SSRF refusal, private source fetched by a node and waiting without one, rollback restores
  content, foreign hash 404, node download and verification, agents of two teams on one node see only their skills,
  team scope refused for another team.
