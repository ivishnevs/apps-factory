Status: decided 2026-11-11 by the owner; implemented 2026-11-12.

## Why

Every upload of an artifact version stays forever. Review rounds re-upload sheets and zips of tens of megabytes, so a
busy project collects gigabytes of superseded working versions nobody opens again. In the hosted cloud this storage
is ours to pay for. Deleting by hand (artifact-duplicates 2.3) does not scale and breaks the links of old tasks.

The owner (2026-11-11): a per-project setting for how long old working versions are kept; in the cloud a hard
ceiling, for example one month; the current flow must not break, but artifacts must not pile up forever; old tasks
must not get broken links: it must be visible that the artifact was archived.

## What Changes

- **Project setting.** `artifact_retention_days` on the project (0, the default, keeps versions forever), edited on
  the project page next to the other settings and through `PATCH /projects/ID`.
- **Cloud ceiling.** Core's `FACTORY_ARTIFACT_RETENTION_MAX_DAYS` (the hosted cloud sets 30; unset or 0 is none).
  The effective retention is the project's setting capped by the ceiling; a project at 0 uses the ceiling. A value
  above the ceiling is refused; the web shows the ceiling (`GET /settings/artifact_retention`).
- **What expires.** Versions older than the retention that are not the latest version of their artifact, not its
  latest approved version, not under an open approval request and not a required input of an open task. Everything
  else (superseded working versions) is archived.
- **Archive, not delete.** An archived version keeps its row: who, when, digest, size, task links, approvals and
  the image listing of an archive. It gets `archived_at` and `archived_reason` (`retention: N days`). The stored
  content is deleted only when no live version, run log, report attachment or skill uses the same digest.
- **Visible, never broken.** Content reads of an archived version answer 410 Gone with when and why and the latest
  version. Listings, task materials and submissions still show the version, marked archived; the web shows
  "Archived DATE" instead of a preview or download. `factory_artifact_get` tells the agent the content is gone and
  names the latest version; an archived version cannot be required or submitted for approval.
- **Daily job.** Retention runs with the daily checks (unmerged branches, orphan changes) per company and records one
  feed event per run with the versions archived per project, the contents deleted and the bytes freed.
- **Restore by upload.** The same content uploaded again under the same id and version restores an archived version.

## Decisions taken in the implementation

- A required input of a task that is not done or cancelled and a version with a pending approval request are kept
  too, so retention never breaks a queued run or an open review.
- A project value above the ceiling is refused (400), not clamped, so nobody believes a value is in force that is not.
- The feed event is one per company per run (no project), listing the counts per project.

## Impact

- Migration 0031: `artifact_versions.archived_at`, `archived_reason`. The project setting lives in the project body.
- Core: `storage/retention.go`, the blob store's `Delete`, 410 answers, the daily check; web: Library, task
  materials and project settings.
- Replaces artifact-duplicates 2.3 (manual cleanup of the game project's duplicates).
