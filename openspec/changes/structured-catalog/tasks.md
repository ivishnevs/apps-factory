## 0. Analysis and decisions

- [ ] 0.1 Analyse the current document model and its users (loader, runs' snapshots, presets, skills, web).
- [x] 0.2 Not needed: decided directly (owner 2026-11-06).
- [x] 0.3 Tables: all configuration in the database, changed through the UI and the API; YAML only for import and
      export (owner 2026-11-06).
- [x] 0.4 Owner approved (2026-11-06) the proposed design: plain rows, change log with undo, row versions, YAML as a file format,
      migration with a config_hash check and a document-API compatibility layer.
- [ ] 0.5 Table design.

## 1. Implementation

- [ ] 1.1 Tables and migrations; import of existing catalogs; tests.
- [ ] 1.2 Core reads and validates the structured catalog; runs keep a snapshot at dispatch.
- [ ] 1.3 Entity API routes (team rename and title among them); web editors move to them.
- [ ] 1.4 YAML import and export; docs.
