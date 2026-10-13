# Action approval verification

Verified 2026-10-12 with real Codex app-server, native Go Worker, Docker Core/NATS and SQLite schema v5.

`make check` passed: race-enabled Go tests and go vet.

Real run: `python3 scripts/approvals-smoke.py --restart-core`.

| Scenario | Approval | Task | Runs | Thread |
|---|---|---|---|---|
| approve | 47481486bdfe425fda6986584f592070 | 8d32a86e6f782c9ff4932e5f4f9dccf4 | 3843832c2f3599342fd3d4d1e023151e, 2175e3b3f2a1d313cd4305a0e3935db8 | 01a131be-699b-7822-a871-e29c1c5ece58 |
| reject | 53a3a297ccb849ce100392aa95445284 | 11964d54728b404ebaa741179fdd0554 | 20d7b3fcd093095d762bc4508443cf3d, 51e5903d8c64d2e58e986e5a023128a6 | 01a131be-e600-73d2-9622-72d10539d743 |

Project: `approvals-smoke-b12ef1b453`.

- Approval persisted across Core restart while the task was BLOCKED.
- Both tasks completed in two attempts using the same thread, then were accepted by owner.
- Approved exact update returned HTTP 200 and tool audit links it to the approval ID.
- Rejected exact update returned HTTP 423; target remained unchanged.
- Repeated owner decision replayed the original result; owner inbox is empty.
- Unit tests also cover changed parameters, agent/project/task scopes, deny precedence, revocation, expiration, always-mode request/status access and generic inbox-answer rejection.
- A decision before runtime completion keeps the writer active; an UNKNOWN run stays quarantined even after approval.
- Existing Foundation run `605503cdb856206b24667f70583db458` remained COMPLETED with `FOUNDATION_OK` after migration.
- Core health is OK; no QUEUED/RUNNING/CANCELLING/UNKNOWN runs remain. Verification Worker was stopped.

Raw local evidence: `.cache/approvals-verification.json`; pre-migration data copy: `.cache/pre-approvals-backup/`.

Scope: exact supported mutation calls. Artifact-version approvals, Decision Registry and external protected executors remain subsequent stages. Read-only calls requiring approval currently fail closed; grants in this stage cover mutations with stable request IDs.
