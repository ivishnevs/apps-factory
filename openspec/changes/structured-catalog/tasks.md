## 0. Analysis and decisions

- [ ] 0.1 Analyse the current document model and its users (loader, runs' snapshots, presets, skills, web).
- [ ] 0.2 Pros and cons of documents vs tables vs a middle way written down with a recommendation.
- [ ] 0.3 Owner decides: keep documents, move to tables or a middle way; if tables, the table design, versioning and rollback model and the migration plan.

## 1. If tables are chosen

- [ ] 1.1 Tables and migrations; import of existing catalogs; tests.
- [ ] 1.2 Core reads and validates the structured catalog; runs keep a snapshot at dispatch.
- [ ] 1.3 Entity API routes (team rename and title among them); web editors move to them.
- [ ] 1.4 YAML import and export; docs.
