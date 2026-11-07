## Current model (0.1, analysed 2026-11-07)

- Storage: `catalog_versions` (company, version, author, note) and `catalog_documents` (company, version, path,
  content): every save copies the whole document set as a new version (migration 0007).
- Kinds in use: `Agent`, `AgentProfile`, `Team`, `Policy`, `NodeHooks`, plus the company files `skills.yaml`,
  `quota_keys.yaml`, `prices.yaml` and prompt files `prompts/NAME.md`.
- Readers: `config.LoadCatalog` resolves documents into `config.Catalog` (agents with their profile, policies,
  skills, instructions); Core keeps one resolved catalog per company in memory (`c.cat`), reloaded on save.
- Writers: `POST /catalog` (put/delete documents on a base version), `/catalog/agents|teams` (web editors, presets),
  `factory_catalog_change` (agents), rollback, company creation (presets copied).
- Runs: `RunSpec.ConfigSnapshot` (the resolved agent JSON) and `config_hash` at dispatch; nothing reads documents later.
- Web: team page, agent form, YAML editor and history read `GET /catalog*` views.

## Table design (0.5)

One row per entity, the entity's spec as JSON in the row (the shape of today's `spec:`), so the loader keeps its
resolution code and only its input changes:

```
catalog_entities(company_id, kind, name, title, spec jsonb, row_version bigint, updated_at, updated_by,
                 PRIMARY KEY (company_id, kind, name))
  kind: agent | profile | team | policy | node_hooks | skills | quota_keys | prices | prompt
  title: the display name (a team or agent can be renamed in the UI without touching references)
catalog_changes(company_id, seq, at, author, person, note,
                changes jsonb  -- [{kind, name, before, after}] for undo
                PRIMARY KEY (company_id, seq))
```

- References stay by `name` (agents → team, profile, policies; teams → lead); a rename of the display name is one row.
- `row_version` gives optimistic concurrency per entity (the editors send it); the whole-catalog base version is
  no longer needed for UI edits.
- Every write appends one `catalog_changes` row with before/after of each entity: history, "undo this change" (apply
  the befores) and "who changed what" (`people-attribution`).
- Runs keep `ConfigSnapshot` and `config_hash` as now.
- YAML stays a file format: export renders the entities as today's documents (`GET /catalog/export`), import parses
  documents into entities in one change (`POST /catalog/import`).

## Migration (owner-approved shape)

1. Create the tables; import each company's current catalog version into entities under a company pause.
2. Load the catalog from both sources and compare `config_hash` per agent; any difference refuses the switch.
3. A per-company switch flag: Core reads entities for switched companies, documents for the rest.
4. The document API (`POST /catalog`, `factory_catalog_change`, the YAML editor) keeps working as a compatibility
   layer: a document change is parsed into entity changes.
5. The old tables stay read-only until every company is switched, then are dropped in a later migration.
