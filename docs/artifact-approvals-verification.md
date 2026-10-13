# Artifact approval verification

Verified 2026-10-12: real Codex app-server, native Go Worker, Docker Core/NATS, SQLite schema v6.

`make check` passed (Go race tests and vet). Script: `scripts/artifact-approvals-smoke.py --restart-core`.

Project: `artifacts-smoke-c669b8d749`.

| Version | Owner decision | Producer task | Consumer task | Consumer outcome |
|---|---|---|---|---|
| v1 | APPROVED | 843169bbf31926d5e31d7bcd9a2cc55a | e1a69354ccfd7671386d99ffce06e5c3 | DONE |
| v2 | REJECTED | 07101bbdea830106edd7e0f67d91e010 | a325fb821abf1d7faeac3c6eb8569829 | BLOCKED |
| v3 | APPROVED | 7c2481e6d19f7cc517b30a946527a6f8 | a325fb821abf1d7faeac3c6eb8569829 | DONE |

- Prepared three SVG fixtures with SHA256-addressed version references.
- Real Codex registered versions, attached structured submissions, and requested nonblocking approval.
- Accepting the producing task did not start QA before content approval.
- Core restart preserved pending v1 approval and the artifact wait state.
- v1 approval started QA with exact pinned id/version/URI and approval revision; QA verified fixture SHA256.
- v2 rejection kept QA BLOCKED with zero runs; v1 approval did not authorize v2.
- Updating the waiting QA task to v3 kept it blocked until a separate v3 approval; QA then verified v3.
- Owner decisions were replay-safe and owner inbox ended empty.
- 8 real Codex runs completed; no active/unknown project runs remain.

Runtime evidence:

| Run | Agent | Purpose | Status |
|---|---|---|---|
| 088f81ab3f90e6bbd73c94602684d73c | demo | task 843169bbf31926d5e31d7bcd9a2cc55a | COMPLETED |
| e904eb7747f180fd234e48e47f79fb2f | demo | approval answer 6dc79ad73d79323232325724ea4ae6fd | COMPLETED |
| 7fd5b70cf14fae585a3fcc3cb963207c | manager | task e1a69354ccfd7671386d99ffce06e5c3 | COMPLETED |
| 59e6108ef0ef60a72adf194ab6cb30bb | demo | task 07101bbdea830106edd7e0f67d91e010 | COMPLETED |
| 14941cec8fc5008a90f39066a0876d1b | demo | approval answer dff80cb22ba8062a06cf190f596b84f2 | COMPLETED |
| 19139b3a5a904872bdaea78f347927f1 | demo | task 7c2481e6d19f7cc517b30a946527a6f8 | COMPLETED |
| d04afdcd5aa9e4917a13789950668e0b | demo | approval answer ea1640eed96050d5ef9812a47d37674e | COMPLETED |
| 06059ac3d79a60283fe010abbf1b2065 | manager | task a325fb821abf1d7faeac3c6eb8569829 | COMPLETED |

Unit checks additionally cover synthetic designer/QA agent configurations, immutable registry and legacy URI references, failed transaction rollback, multiple required versions, denial through implicit submission registration, expired/reconsidered requests, revoked queued claims and preserved manual/UNKNOWN blocks.

Raw evidence: `.cache/artifact-approvals-verification.json`, `.cache/artifact-final-verification.json`; pre-migration backup: `.cache/pre-artifact-approvals-backup/`.

Approved content authorizes starts until revoke. Already running work retains its startup-authorized input snapshot. Core stores references, not content blobs; producers must use immutable/content-addressed URIs and bump versions for changed content.

Final deployment: Core rebuilt with the legacy-reference guard and multiple-version dependencies; doctor/CLI artifact get passed. Foundation result and previous action approval/rejection survived migration. No active/unknown runs remain; native verification Worker stopped.
