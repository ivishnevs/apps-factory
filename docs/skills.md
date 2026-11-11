# Skills

Skills are [Agent Skills](https://agentskills.io) directories (`<name>/SKILL.md` with frontmatter `name`, equal to the
directory, and `description`; optional `license`, `compatibility`, `metadata`, `allowed-tools`; supporting files in
`scripts/`, `references/`, `assets/`). Discovery categories come from `metadata.categories` (comma separated), else
`metadata.tags`. There is no Factory-specific manifest (skill-registry).

**Core owns skill contents.** Every indexed skill version is a deterministic tar of its directory in Core's blobs,
identified by its content hash (`skills.Hash`, `sha256:…`). Factory's own skills are the release's `skills/`, indexed
at Core start as source `factory`. A company adds its own sources once, on the Skills page, the API or the CLI
(owners and admins):

- **git, public** (`access: public`): Core fetches it, https only, public addresses only (checked when connecting),
  shallow, no submodules, LFS or hooks, within 2 minutes and 64 MiB;
- **git, private** (`access: node`): an online node of the company fetches it with its own git access (as it clones
  project repositories) and uploads the tree; without an online node the source shows `waiting_for_node`. Core keeps
  no git credentials;
- **upload**: an archive (zip, tar.gz) or a folder; uploading again updates it.

The indexer finds skills by layout, first match: a Claude plugin marketplace (`.claude-plugin/marketplace.json`, each
plugin's skills directory), else `skills/*/SKILL.md`, `.agents/skills/*/SKILL.md`, `.claude/skills/*/SKILL.md`
(symlinked skill directories are skipped), else a `SKILL.md` at the root (or `subpath`), else skill directories at the
root. Each skill is validated (name = directory, regular files only, at most 512 files, 1 MiB each, 16 MiB in all); an
invalid one shows its error in the library and does not stop the others.

**Installed skills** are the company's `skills.yaml` in its catalog (a catalog version, with history and rollback):

```yaml
skills:
  openspec: {source: factory}                       # Factory's skill: the current indexed version
  brand:    {source: acme-skills, commit: 3f2a…, hash: sha256:…, scope: team:saas}
```

A skill names its source and, except for `factory`, its hash; description and categories come from the index unless
set here. A name is unique per company. `scope: team:NAME` (only that team's agents) or `scope: agent:NAME` limits who
may include or discover it; including a skill outside its scope fails loading. `path:` entries are gone.
Installing is availability, not use: an agent has a skill only when its `spec.skills.include` names it. The web Skills
page says so: the scope badge reads "Any agent may use" (or "Team NAME only", "Agent NAME only"), and each installed
skill lists the agents that include it ("Used by …", or "Not given to any agent yet") (skill-use-visible).

Updates: syncing a `pinned` source (`update: pinned`) shows an available update of each installed skill with its file
changes (`GET /skills/installed`); installing it saves a catalog version. A `track` source installs new versions on
sync (a catalog version authored `sync`). Git sources sync once a day; running runs keep their pinned hashes.

```sh
factory skills sources                                    # sources and their state
factory skills sources add https://github.com/acme/agent-skills -ref main [-access node] [-update track]
factory skills upload brand-kit ./skills.zip
factory skills sync acme-skills
factory skills library brand                              # search the library
factory skills install acme-skills brand [-scope team:saas]
factory skills installed [-team saas]
factory skills uninstall brand                            # refused while an agent includes it
```

API (owners and admins change, every role reads): `GET/POST /skills/sources`, `POST /skills/sources/upload`,
`POST /skills/sources/{id}/sync`, `DELETE /skills/sources/{id}`, `GET /skills/library?q=&source=&category=`,
`GET /skills/library/{source}/{name}/files[/{path}]`, `GET /skills/installed[?team=]`, `POST /skills/install`
`{base, source, name, scope}`, `POST /skills/uninstall` `{base, name}`.

## Agents and runs

 SKILL.md requires YAML frontmatter with matching name and nonempty
description; standard extra frontmatter fields are allowed. Prompt bodies are not included in run
instructions. Names in skills.include must exist. Enabled discovery adds skills matching any
configured category; disabled discovery adds none. Discovery with no categories adds none.

```sh
factory skills list demo
factory skills search demo spec documentation
factory skills read demo openspec
factory skills read demo openspec references/conventions.md
```

Owner CLI and the web read the authenticated Core API using FACTORY_URL/FACTORY_TOKEN. Runs do not read skills
through Core: their skills reach the runtime natively (below), and the run instructions list the skills (name,
description) and say that a skill's scripts and references are in the skill's directory (native-skills-in-runs).

HTTP endpoints: GET /agents/{agent}/skills (optional q/category query),
GET /agents/{agent}/skills/{skill} (optional resource query, defaults to SKILL.md).
Read is allowed only for an explicitly included or discoverable skill. All operations also check
factory.skills.list/search/read against tool filters and policies. Search results contain metadata.
Supporting files in references/, scripts/, and other skill subdirectories can be retrieved on demand.
The API returns content; it does not execute scripts. Resource paths must remain inside the skill's
own directory, including symlinks. Only regular files <= 1 MiB can be read through the API.

Runs on a node get their offered skills from Core: Core pins each skill's hash in the run's agent snapshot
(`skill_hashes`); the node downloads a pinned hash once (`GET /runs/{run}/skills/{hash}`, signed, only hashes pinned on
that run) into its store `<state_dir>/skills/store/sha256-…` (verified, read-only, removed after 14 days unused) and
copies the run's skills into a per-run view the drivers install from. A hash that does not verify, or that Core does
not hold, ends the run `setup_failed` before the runtime starts; a skill without a pin (a Core without a skill index)
is skipped with a warning. Claude Code reads the view as project skills (`<cwd>/.claude/skills/<name>`, its Skill tool
shows the skill's directory); Codex gets the view as an extra skills root (`skills/extraRoots/set`) and lists each skill
with its path. Factory MCP offers runs no skills tools.
