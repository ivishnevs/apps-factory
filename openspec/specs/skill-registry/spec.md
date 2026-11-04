# skill-registry Specification

## Purpose
Keep skill contents in Core, indexed from the release and from sources a company manages once (public git, private
git through its node, uploads), installed into its catalog by hash and delivered to the nodes that run them.

## Requirements

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

### Requirement: Skill sources
An owner or admin SHALL be able to add a skill source once per company: a public git repository fetched by Core, a private git repository fetched by an online node of the company and uploaded to Core, or an uploaded archive. Core SHALL fetch only over `https` from public addresses, shallowly, without submodules, LFS or hooks, within time and size limits, and SHALL keep no git credentials.

#### Scenario: Private address refused
- **WHEN** an owner adds a public source whose host resolves to a private address
- **THEN** Core SHALL refuse the sync and record the error on the source

#### Scenario: Private repository
- **WHEN** an owner adds a private repository and a node of the company is online
- **THEN** the node SHALL fetch it with its own git access and upload the tree, and Core SHALL index it

#### Scenario: No node online
- **WHEN** a private source is synced and no node of the company is online
- **THEN** the sync SHALL wait and the source SHALL show that it needs an online node

### Requirement: Deterministic indexing
Indexing SHALL discover skills by the first matching layout: `.claude-plugin/marketplace.json`, then `skills/*/SKILL.md`, `.claude/skills/*/SKILL.md`, `.agents/skills/*/SKILL.md`, then a root `SKILL.md`. Each skill SHALL be validated against the Agent Skills format and the size limits; invalid skills SHALL be reported individually and the others indexed. Indexing the same tree twice SHALL give the same names and hashes.

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
Sources SHALL be managed on the company's Skills page, API and CLI. The agent editor SHALL offer only installed skills available to the agent's team and SHALL NOT accept a repository URL.

#### Scenario: Picking skills for a new agent
- **WHEN** an owner creates an agent in team `gamedev`
- **THEN** the skill picker SHALL list installed Factory and company skills not scoped to another team or agent

### Requirement: Team and agent scope
An installed skill with `scope: team:<name>` SHALL be available only to agents of that team, and one with `scope: agent:<name>` only to that agent.

#### Scenario: Other team includes a scoped skill
- **WHEN** an agent of team `gamedev` includes a skill scoped to team `saas`
- **THEN** catalog validation SHALL fail

### Requirement: Delivery to nodes by hash
A node SHALL obtain a run's skills from Core by the hashes pinned on the run, keep them in a content-addressed store under `~/.app-factory/skills`, verify each with `skills.Hash` before use, and give each run a view containing only its agent's skills. Core SHALL serve a skill blob only to the node of a run that pinned its hash.

#### Scenario: Node without the skill
- **WHEN** a run pins a hash the node's store lacks
- **THEN** the node SHALL download, verify and store it before the runtime starts

#### Scenario: Blob of another run
- **WHEN** a node requests a hash not pinned on the run it names
- **THEN** Core SHALL answer not found

#### Scenario: Agents see only their skills
- **WHEN** runs of agents of two teams run on one node
- **THEN** each runtime SHALL see only its own agent's skills
