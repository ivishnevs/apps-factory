# Task Engine tasks

- [x] Add transactional version-2 SQLite migration and preserve migration-1 runs.
- [x] Add registered projects with immutable workspace and optimistic metadata updates.
- [x] Add task model, dependencies, parent/OpenSpec references and validated lifecycle actions.
- [x] Validate DAG cycles, cross-project links, stale versions and idempotent mutation replay.
- [x] Dispatch task/attempt/run/outbox atomically with writer and agent reservation checks.
- [x] Snapshot resolved configuration/hash, offered skills and independent task sessions per attempt.
- [x] Project terminal run outcomes, cancellation, expiry and reconciliation into tasks transactionally.
- [x] Add implicit/explicit submission, review/rework, comments and versioned artifact references.
- [x] Bound known-failure retries by snapshotted max_retries; never auto-retry UNKNOWN/reconciliation.
- [x] Add authenticated task/project API/CLI and named-agent policy/communication validation.
- [x] Add durable audit events with bounded broker notifications and contextual result excerpts.
- [x] Pass unit/race/static checks, JetStream restart/cancellation regressions and migration tests.
- [x] Run real Codex two-task chain with review gating, Core restart and distinct thread IDs.
- [x] Back up/migrate the working database, update services and document limits/next stage.

Verification: docs/task-engine-verification.md. Factory MCP, questions, parameter-bound approval
workflow, artifact approvals and report persistence remain later stages.
