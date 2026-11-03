## Context

See proposal. Related: `agent-config` (skills metadata only at start, reads scoped to the agent's skills),
`company-self-service` (stored catalog, D5 operator-only skill contents), `runtime-drivers` 2.3 (node
materialization verified by `skills.Hash`), `core-owned-project-data` (`internal/blobstore`, company-scoped
digests), `core-secret-store` (private repository credentials), `user-accounts` (company per request),
`team-builder` (presets reference built-in skills).

## Decisions (owner 2026-11-03)

### D1. Formats: existing standards, no own manifest
A skill is an Agent Skills directory (agentskills.io): `<name>/SKILL.md` with frontmatter `name` (equal to the
directory, `[a-z0-9-]`, ≤ 64), `description` (≤ 1024), optional `license`, `compatibility`, `metadata`
(string map), `allowed-tools`; supporting files in `scripts/`, `references/`, `assets/`. Categories for discovery
come from `metadata.categories` (comma separated), else `metadata.tags`. A repository is discovered by the first
layout that matches (D3); no Factory-specific manifest is required. Our default skills live in a repository in
the same format.

### D2. Core owns skill contents
Every indexed skill version is a tar of its directory stored in `internal/blobstore` under the key
`skills.Hash` (the hash nodes already verify). Built-in skills are stored once under the reserved scope
`factory`; a company's own under its company. Core reads (`factory_skills_read`, owner UI) serve the blob, not
disk. The operator's disk registry (`path:` entries) stays only for self-hosted catalogs.

### D3. Sources and the indexer
`skill_sources (company_id, id, kind builtin|git, url, ref, subpath, secret, update pinned|track, commit,
synced_at, status, error)`. A sync:
1. Fetch: `https`/`ssh` only, public addresses only (no private/link-local IPs), shallow (`--depth 1`), no
   submodules, no LFS, hooks disabled, time and size limits, in a fresh directory; credential from the secret
   store for private repositories. Resolve the ref to a commit SHA.
2. Discover (first match wins): `.claude-plugin/marketplace.json` → its plugins' skill directories (plugin
   `skills` field, default `skills/`); else `skills/*/SKILL.md`, `.claude/skills/*/SKILL.md`,
   `.agents/skills/*/SKILL.md`; else `SKILL.md` at the root (or `subpath`): a single-skill repository.
3. Validate each with today's rules (frontmatter, name = directory, regular files only, no symlinks, ≤ 512 files,
   ≤ 1 MiB each, ≤ 16 MiB total); an invalid skill is a per-skill error, the others are indexed.
4. Hash, store the blob, write `skill_index (company_id, source_id, name, commit, hash, description, categories,
   frontmatter, files)`.
Deterministic: the same commit gives the same index and hashes. Search is substring/full-text over name,
description and categories. Built-in source: indexed by Core at start from the release's default skills.

### D4. Installed skills live in the catalog
`skills.yaml` gains sources and installed skills pinned by hash:
```yaml
sources:
  acme: {git: https://github.com/acme/agent-skills, ref: main, secret: acme-github, update: pinned}
skills:
  openspec:    {source: factory, hash: sha256:…}
  brand-voice: {source: acme, commit: 3f2a…, hash: sha256:…, scope: agent:writer}
```
Description and categories come from the index (overridable). The loader resolves a skill by its hash in the
blob store instead of a file path. Saving, history and rollback of catalog versions therefore cover skills. A
skill name is unique per company (Claude materializes `.claude/skills/<name>` and the name must equal the
directory); a conflict between sources is resolved by choosing which to install. No `source/name` namespaces.

### D5. Managed once, picked in the agent form
Sources are added, synced and removed on the Skills page (and API/CLI), once per company. Installing a skill
writes `skills.yaml`. The agent editor offers installed skills only (Factory and the company's own), with a
link to the Skills page. A source can be attached to another company the same user owns in one action (copies
the source definition; blobs are shared by hash within the blob store's company scope rules).

### D6. Updates
A sync of a `pinned` source shows available updates (new hash, file diff); installing one saves a catalog
version. A `track` source installs new hashes on sync automatically (a catalog version authored `sync`). Running
runs keep their pinned `skill_hashes`.

### D7. Delivery to nodes: store plus per-run view
Core pins `skill_hashes` in the run snapshot (exists). `GET /skills/blobs/{hash}` with the run credential serves
only hashes pinned on that run. The node keeps `~/.app-factory/skills/store/sha256-<hex>/` (read-only,
immutable): a missing hash is downloaded to `tmp/`, unpacked, verified with `skills.Hash`, then renamed into the
store. Each run gets a view with only its agent's skills (`~/.app-factory/runs/<run>/skills/<name>`); the
drivers install from it (Claude: `<cwd>/.claude/skills/<name>` with `.factory-skill` and per-worktree excludes,
as today; Codex: from the same view). The user's own `~/.claude/skills` is never touched. GC removes store
entries unused for N days and not pinned by an active run. `skills_root` remains a fallback for self-hosted
nodes.

### D8. Per-agent scope
An installed skill may carry `scope: agent:<name>`: only that agent may include it, and discovery offers it to
no other agent. Loading fails otherwise.

## Risks / Trade-offs

- Fetching user-provided URLs from Core: SSRF and resource abuse are contained by D3's limits; git runs as a
  subprocess without hooks.
- Skill content is untrusted prompt and script material of the client; it runs on the client's own nodes under
  the agent's policies (as today). Built-in skills are ours.
- Core storage grows with skill versions; blobs are deduplicated by hash and old unreferenced versions can be
  collected later.
