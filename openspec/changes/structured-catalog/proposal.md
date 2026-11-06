Status: decided 2026-11-06 by the owner: all configuration moves to structured database tables, editable through
the UI and the API. Design and migration plan to do. Nothing implemented.

## Why

The company catalog lives in Postgres, but as versioned YAML documents (`catalog_versions`, `catalog_documents`:
a path and YAML text per document) in the format the file catalog had. Every change to one field means
rewriting documents: renaming a team (2026-11-05) took rewriting the team, profile and policy documents, every
agent's `spec.team` and every skill scope in `skills.yaml`. Teams have no display title apart from their
identifier. This is hard to support and to extend: a usual feature such as "rename a team" should be an update of
one column in one row.

## The question: keep the document model or move to tables?

The YAML documents were designed on purpose, after Kubernetes manifests, so the catalog could be kept and edited
locally in a folder (and in git). App Factory is now a web SaaS, so it is not obvious that this still pays off: it
may be legacy to remove, or it may still have practical value. Weigh both before deciding.

For keeping documents (or documents as one face of the catalog):
- One validated, versioned snapshot of the whole catalog: rollback, diff between versions and a run's snapshot are
  simple.
- GitOps and local editing: keep a company's catalog in a repository, review changes in pull requests, apply with
  the CLI; export, backup and moving a team between companies come for free.
- One loader and one validation path for presets, the default company's seed and companies' catalogs.
- Agents and advanced users can read and write the whole configuration as text.

For structured tables:
- Usual product features are usual updates: rename a team, change a lead, edit one field; references by id follow.
- Queries, constraints and indexes in the database instead of parsing documents; simpler web editors and APIs.
- Easier to reason about for new developers; fewer "rewrite five documents for one change" cases.
- There is no GitOps use today: customers edit in the web UI, not in git.

Middle ways to consider: tables as the storage with YAML import and export (and optional git sync later); or
documents as the storage with entity APIs and display names on top, without a migration.

## What Changes if tables are chosen (to be analysed)

- Move the catalog to structured tables: teams, agents, profiles, policies and rules, prompts, installed skills and
  scopes, quota keys, node hooks, external MCP servers, company settings, with stable ids and foreign keys
  (agent → team, agent → profile, skill scope → team or agent), so a rename changes one row and references follow.
- Display names separate from identifiers (team title, agent title), editable in the UI.
- Keep what the document model gives today: versions with author and note, rollback, a run's configuration
  snapshot at dispatch, validation before save, apply without restart. Options to weigh: history tables per
  entity, an append-only change log, or snapshots of the structured rows per version.
- Presets stay templates in the repository and are copied into the tables; the `configs/` seed of the default
  company is imported once.
- The YAML form may remain as an import and export format (backup, moving a team between companies), not as the
  storage.

## Open questions

- Keep documents, move to tables, or a middle way (above); is there a real customer need for GitOps or local
  editing of the catalog.

- How versions and rollback work on structured rows; what a run snapshot captures.
- Migration of existing companies' document catalogs and of the loader's validation rules.
- Which API and UI changes follow (entity routes instead of document edits).

## Impact

Storage (new migrations, data migration), the catalog loader and Core's per-company catalog cache, catalog,
preset and skill routes, web catalog editors, `company-catalog`, `team-builder` and `skill-registry` specs.

## Decision (2026-11-06)

All configuration (teams, agents, profiles, policies, prompts, skills and their scopes, quota keys, node hooks, MCP
servers, company and project settings) lives in structured database tables with stable ids and references, and is
changed through the UI and the API (entity by entity: an agent's parameter, a team's title), not by editing YAML
documents. YAML stays only as import and export (backup, moving a team). Versions with author and note, rollback, and
the run's snapshot at dispatch are kept. This is the base for the agent catalog and ready teams
(`default-agent-library`) and for scoped catalog changes by leads (`agent-company-authority`).

## Design (approved by the owner 2026-11-06)

**Plain rows, edited in place.** Changing an agent is an update of its row (or rows: its skills, its rules); the UI
and the API change one field of one entity. No whole-catalog versions.

**What replaces versions, and why each part is kept:**
- **Change log (append-only):** every change writes who, when, which entity, before and after, and the note. Needed
  for audit (`people-attribution`: who changed the producer's prompt) and for undo.
- **Undo instead of rollback of the catalog:** "revert this change" applies its before-values (refused if the entity
  changed since, with the conflict shown).
- **Row version for concurrent edits:** each entity has a version number; a save with an old one is refused, so two
  people (or an agent and a person) do not overwrite each other silently. Agents' catalog changes use it the way they
  use the catalog base version today.
- **Runs need nothing more:** a run already stores its resolved agent configuration (`config_snapshot`,
  `config_hash`) at dispatch, so what it ran with stays known after any edit.

**YAML only as a file format:** export of a team or the whole company (backup, moving a team, sharing a preset) and
import of such a file; never the place where the configuration lives.

## Migration without breaking the running company

1. Tables are created next to the current `catalog_versions` / `catalog_documents`; nothing is removed.
2. A one-time import per company (under the company pause, between runs) turns the current catalog version into rows.
3. Check before switching: for every agent, the resolved configuration from the rows must produce the same
   `config_hash` as from the documents; any difference stops the switch and is shown.
4. Core switches the company to the tables (per company flag); live and queued runs carry their snapshots and are not
   affected.
5. Compatibility while the clients move: the document API (`GET /catalog`, `/catalog/document`, `POST /catalog`,
   the agents' `factory_catalog_change`) keeps working on top of the rows — documents are rendered from rows, a
   document change is parsed and applied as row changes — until the web, TUI and agent tools use the entity API.
6. The document tables stay read-only for a while (switch back possible), then are dropped.
