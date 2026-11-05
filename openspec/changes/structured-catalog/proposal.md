Status: open question recorded 2026-11-05 by the owner; not analysed or decided. Nothing implemented. The
answer may be "keep documents": the change exists to weigh both ways and give a reasoned answer.

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
