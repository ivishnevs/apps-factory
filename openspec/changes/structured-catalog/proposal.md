Status: idea recorded 2026-11-05 by the owner; not analysed or decided yet. Nothing implemented.

## Why

The company catalog lives in Postgres, but as versioned YAML documents (`catalog_versions`, `catalog_documents`:
a path and YAML text per document) in the format the file catalog had. Every change to one field means
rewriting documents: renaming a team (2026-11-05) took rewriting the team, profile and policy documents, every
agent's `spec.team` and every skill scope in `skills.yaml`. Teams have no display title apart from their
identifier. This is hard to support and to extend: a usual feature such as "rename a team" should be an update of
one column in one row.

## What Changes (idea, to be analysed)

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

- How versions and rollback work on structured rows; what a run snapshot captures.
- Migration of existing companies' document catalogs and of the loader's validation rules.
- Which API and UI changes follow (entity routes instead of document edits).

## Impact

Storage (new migrations, data migration), the catalog loader and Core's per-company catalog cache, catalog,
preset and skill routes, web catalog editors, `company-catalog`, `team-builder` and `skill-registry` specs.
