Done (owner 2026-11-03: D1–D9 confirmed, Core owns skill contents). No migration (not released).
Depends on `node-identity` (signed node requests) and `company-teams` (team scope). Order: blobs and delivery by
hash with the built-in source (default skills on any node), then sources, indexer and UI, then updates.

## 1. Decide
- [x] 1.1 Owner confirms Core owns skill contents, reversing company-self-service D5 for skills (D2).
- [x] 1.2 Owner confirms sources per company managed once on a Skills page, picker only in the agent form (D6).
- [x] 1.3 Owner confirms private repositories fetched by the company's node, no Core-held git credentials (D3).
- [x] 1.4 Owner confirms the node store `~/.app-factory/skills` with a per-run view (D8).

## 2. Specs and docs
- [x] 2.1 Delta specs `skill-registry`, `agent-config`, `company-catalog`; `openspec validate --strict`.
- [x] 2.2 Docs: `skills.md` (formats, sources, installed skills, scope, delivery), `catalog.md` (skills.yaml shape,
  D5 change), `development.md` (node store, GC, `skills_root` removed). Done: `docs/skills.md` (formats, sources, indexer, installed skills, scope, updates, CLI/API, delivery), `catalog.md`, `development.md`.

## 3. Blobs and delivery
- [x] 3.1 Skill blobs in `internal/blobstore` keyed by `skills.Hash` (tar of the directory, deterministic);
  reserved scope `factory` for built-in skills. Done: `skills.Pack`/`Unpack`/`ReadFile` (deterministic tar, verified unpack), blobs of scope `factory` or the company; the index maps a skill hash to its blob (migration 0016).
- [x] 3.2 Built-in source: the release's default skills indexed at Core start; templates and presets use
  `source: factory` instead of `../../skills/...`. Done: `Core.IndexBuiltinSkills` at catalog load; categories moved into the built-in SKILL.md frontmatter (`metadata.categories`); templates and seeds use `{source: factory}` (hash resolved from the index; offline from the checkout, `config.DiskSkills`).
- [x] 3.3 Loader: installed skills by source and hash; Core reads served from blobs; `skill_hashes` pinned from the
  installed hashes; `path:` entries removed. Done: `skills.Registry` holds installed skills (source, hash, commit, scope); `config.SkillResolver`/`LoadStoredCatalogWith`; reads via `Core.skillFile` from blobs; pins from the registry; `path:` refused.
- [x] 3.4 `GET /runs/{run}/skills/{hash}` for signed node requests (`node-identity`), only hashes pinned on the named run
  of that node. Done: `Core.runSkill` behind the node gateway (only hashes pinned on the run; the gateway refuses another node's run).
- [x] 3.5 Node store `~/.app-factory/skills/store/sha256-*` (download to `tmp/`, verify, atomic rename, read-only),
  per-run view, drivers install from the view; GC of unused entries; `skills_root` removed. Done: `worker.storedSkill` (store under `<state_dir>/skills/store`, not `~/.app-factory`: one store per node state, testable), per-run view, `GCSkillStore` (14 days, at start and daily), `skills_root` and `LoadSkills` removed; an unpinned skill is skipped with a warning; `TestSkillsPinned`, `TestPackUnpack`, `TestDiscoverAndIndex`.

## 4. Sources and indexer
- [x] 4.1 Schema: `skill_sources`, `skill_index`, source sync jobs. Done: migration 0016 (`skill_sources` per scope, `skill_index` per source/name/hash with the blob, `skill_sync_jobs`).
- [x] 4.2 Public fetch by Core: `https`, public addresses only, shallow, no submodules/LFS/hooks, limits; ref → commit. Done: `skills.FetchPublic` with go-git (Core's image has no git): https, public addresses checked at dial time, shallow, no submodules or tags, 2 min, 64 MiB.
- [x] 4.3 Private fetch by a node: sync job to an online node of the company (heartbeat answer; the poll answer of
  `http-node-transport` when it exists, never NATS), fetch with the node's git access, packed
  tree uploaded with a signed request; waiting state without an online node. Done: sync jobs in the poll answer (`sync_jobs`), `runner/sync.go` (node git, no hooks or prompts, `PackTree`), signed `POST /skills/sync-jobs/{id}`; `waiting_for_node` without an online node; `TestPrivateSourceSync`.
- [x] 4.4 Upload of an archive or folder (owner/admin). Done: `POST /skills/sources/upload` (zip, tar.gz, tar or `files[]`), `factory skills upload`.
- [x] 4.5 Discovery by layout (D4), validation, hashing, per-skill errors; fixtures for each layout. Done: `skills.Discover`/`Index` (plus skill directories at the root for uploads); `TestDiscoverAndIndex`.
- [x] 4.6 API and CLI (routes of design D10): sources (add, sync, remove), library (search, read file), install/uninstall (catalog version),
  `factory skills sources|sync|library|install`. Done: `internal/core/skill_sources.go`, `cmd/factory/skills.go`; `TestSkillSources`.

## 5. Updates and scope
- [x] 5.1 `pinned` updates with file diff; `track` installs on sync (author `sync`); periodic sync. Done: `GET /skills/installed` update with file changes; track installs on sync (author `sync`); daily sync checked hourly.
- [x] 5.2 `scope: team:<name>` and `scope: agent:<name>` validation, discovery and picker filtering. Done: `skills.Skill.InScope`, loader refusal and discovery filter, `installed?team=`; `TestSkillsPerCompany`.

## 6. Web UI
- [x] 6.1 Skills page: Sources (link or upload, sync, status), Library (render SKILL.md, file tree), Installed
  (versions, updates, scope). Progress: `web/src/app/c/[company]/skills/page.tsx` written on the routes of design D10 (shows "not available" until 4.6); check against the backend when it lands. Done: checked against the routes (shapes match); not exercised in a browser — the page moves with `saas-web`.
- [x] 6.2 Agent editor skill picker: installed skills available to the team, discovery categories, link to Skills. Progress: `web/src/components/app/team/skill-picker.tsx` on the team page (`GET /skills/installed?team=`, saves `spec.skills.include` of the agent document; `GET /catalog/teams` gives each agent `document` and `skills`); moves into the agent form of team-builder 5.2. Done: as 6.1.

## 7. Verification
- [x] 7.1 `make check` green plus NATS tests with `-p 1` while NATS exists (`http-node-transport` removes them); tests: deterministic index of the same tree, invalid skill
  does not block others, SSRF refusal, private source fetched by a node and waiting without one, rollback restores
  content, foreign hash 404, node download and verification, agents of two teams on one node see only their skills,
  team scope refused for another team. Done: `make check` green (NATS is gone); `TestSkillSources` (SSRF refusal, invalid skill not blocking, install, pinned/track updates, rollback content, private source via node, upload, foreign run), `TestSkillsPinned` (download, verification, foreign hash), `TestSkillsPerCompany` (team scope), `TestPrivateSourceSync`.