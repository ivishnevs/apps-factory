## 1. Core

- [x] 1.1 Migration 0031: `archived_at`, `archived_reason` on artifact versions; `artifact_retention_days` in the
      project body, validated against `FACTORY_ARTIFACT_RETENTION_MAX_DAYS`; `GET /settings/artifact_retention`.
- [x] 1.2 Retention run per company: archive superseded versions past the effective retention (not the latest, not
      the latest approved, not pending approval, not a required input of an open task); tests.
- [x] 1.3 Delete content only archived versions use (blob store `Delete`, row lock, re-check; an upload racing the
      delete is refused and retried); tests.
- [x] 1.4 Content, archive entry and run blob reads of an archived version answer 410 Gone with when, why and the
      latest version; `factory_artifact_get` notes it; requiring or submitting an archived version is refused; the
      same content uploaded again restores it; tests.
- [x] 1.5 Daily check runs retention with the unmerged-branch and orphan-change checks; one feed event
      `artifact.retention` per run with counts and bytes freed; tests.

## 2. Web

- [x] 2.1 Library and task materials: an archived version shows "Archived DATE", opens a note instead of a preview
      or download; a Library card counts its archived versions.
- [x] 2.2 Project page settings: "Keep old artifact versions (days)" with the ceiling.

## 3. Docs

- [x] 3.1 docs/artifact-approvals.md: retention.
