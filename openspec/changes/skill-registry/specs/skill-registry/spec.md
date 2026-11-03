## ADDED Requirements

### Requirement: Core owns skill contents
Every skill version offered to agents SHALL be stored in Core as a content-addressed blob keyed by its `skills.Hash`. Core reads of skill resources and node delivery SHALL use these blobs, not files on the Core or node host.

#### Scenario: Skill read through Core
- **WHEN** an agent reads `references/conventions.md` of an installed skill
- **THEN** Core SHALL serve it from the blob of the installed hash

### Requirement: Built-in skills for every company
Every company SHALL have the built-in Factory source, indexed by Core from the release's default skills, and SHALL be able to install its skills without adding a source.

#### Scenario: New company
- **WHEN** a company is created
- **THEN** the Factory skills SHALL be listed in its library

### Requirement: Git skill sources
A company owner SHALL be able to add a git repository as a skill source by URL, optional ref, subpath and secret. Core SHALL fetch only over `https` or `ssh` to public addresses, shallowly, without submodules, LFS or hooks, within time and size limits.

#### Scenario: Private address refused
- **WHEN** an owner adds a source whose host resolves to a private address
- **THEN** Core SHALL refuse the sync and record the error on the source

### Requirement: Deterministic indexing
A sync SHALL resolve the ref to a commit and discover skills by the first matching layout: `.claude-plugin/marketplace.json`, then `skills/*/SKILL.md`, `.claude/skills/*/SKILL.md`, `.agents/skills/*/SKILL.md`, then a root `SKILL.md`. Each skill SHALL be validated against the Agent Skills format and the size limits; invalid skills SHALL be reported individually and the others indexed. Indexing the same commit twice SHALL give the same names and hashes.

#### Scenario: Collection with one broken skill
- **WHEN** a repository has `skills/a/SKILL.md` valid and `skills/b/SKILL.md` whose name differs from its directory
- **THEN** `a` SHALL be indexed and `b` SHALL be reported as an error of the source

#### Scenario: Single-skill repository
- **WHEN** a repository has only `SKILL.md` at its root
- **THEN** it SHALL be indexed as one skill

### Requirement: Installed skills pinned in the catalog
Installed skills SHALL be catalog entries naming their source and content hash, so a catalog version determines the exact skill contents. Skill names SHALL be unique within a company.

#### Scenario: Rollback restores skill content
- **WHEN** an owner rolls the catalog back to a version that pinned an older hash of a skill
- **THEN** new runs SHALL get the older content

#### Scenario: Name conflict
- **WHEN** an owner installs `review` from a source while `review` from another source is installed
- **THEN** Core SHALL refuse the install until the other one is removed

### Requirement: Updates
A sync of a `pinned` source SHALL report available updates without changing installed skills; a sync of a `track` source SHALL install new hashes as a new catalog version. Running runs SHALL keep the hashes pinned at dispatch.

#### Scenario: Pinned update
- **WHEN** a pinned source's repository changes a skill and the owner syncs
- **THEN** the skill SHALL be shown with an update and agents SHALL keep the installed hash until the owner installs the update

### Requirement: Skills managed apart from agents
Sources SHALL be managed on the company's Skills page, API and CLI. The agent editor SHALL offer only installed skills, built-in and the company's own.

#### Scenario: Picking skills for a new agent
- **WHEN** an owner creates an agent
- **THEN** the skill picker SHALL list installed Factory and company skills and SHALL NOT accept a repository URL

### Requirement: Per-agent scope
An installed skill with `scope: agent:<name>` SHALL be available only to that agent.

#### Scenario: Other agent includes a scoped skill
- **WHEN** agent `qa` includes a skill scoped to agent `writer`
- **THEN** catalog validation SHALL fail

### Requirement: Delivery to nodes by hash
A node SHALL obtain a run's skills from Core by the hashes pinned on the run, keep them in a content-addressed store under `~/.app-factory/skills`, verify each with `skills.Hash` before use, and give each run a view containing only its agent's skills. Core SHALL serve a skill blob only to a run that pinned its hash.

#### Scenario: Node without the skill
- **WHEN** a run pins a hash the node's store lacks
- **THEN** the node SHALL download, verify and store it before the runtime starts

#### Scenario: Blob of another run
- **WHEN** a run credential requests a hash not pinned on that run
- **THEN** Core SHALL answer not found

#### Scenario: Agents see only their skills
- **WHEN** two runs of different agents run on one node
- **THEN** each runtime SHALL see only its own agent's skills
