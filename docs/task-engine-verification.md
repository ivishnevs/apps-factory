# Task Engine verification — 2026-10-12

- `make check` passed: Go unit tests with race detector plus go vet.
- Native factory/codex-worker and Core Docker image build successfully.
- Storage tests cover v1->v2 migration preserving old runs, DAG/cross-project rejection,
  task/project optimistic versions, request replay/key conflicts, atomic duplicate dispatch,
  review gating, rejection/rework/session resume, UNKNOWN quarantine/reconciliation, queued/running
  cancellation and bounded broker notifications for a 700 KiB result.
- Core tests cover priority/agent reservation, configuration snapshots, retry limits and HTTP
  task permissions (creation/assignment denied, approval-required operations blocked, invalid status rejected).
- Real isolated JetStream + fake app-server tests pass for Foundation, cancellation and Task Engine
  restart. The new restart test closes/reopens SQLite and restarts Core/Worker before accepting a
  prerequisite; the dependent task executes exactly once afterward.
- Working Core was stopped and its complete data directory backed up to
  .cache/task-engine-backup.ilTA3L/data before migration. Existing Foundation result FOUNDATION_OK
  remained readable afterward. No volumes were deleted.

## Real Codex chain

`scripts/task-smoke.py --restart-core` completed using Codex 0.162.1 and the owner's existing
ChatGPT login, with native Worker and Core/JetStream in Docker.

Project: task-smoke-172e060df5.

| Task | Result | State | Attempts | Version |
| --- | --- | --- | --- | --- |
| b15b6f59e003ab061df8aabe9782a21c | TASK_ONE_OK | DONE | 1 | 5 |
| e0b12b1a3c96c37143205887007ee49b | TASK_TWO_OK | DONE | 1 | 5 |

The second task stayed READY with zero attempts while the first awaited review. Core restarted
at that point. Accepting the first task released its dependent. Both results were then explicitly
accepted by the smoke harness; no runtime completion directly marked DONE. Eleven project audit
events persisted. Both attempts have configuration hashes/snapshots and distinct Codex threads:
01a13171-d7fb-7e21-88aa-40d4f74d7f4e and 01a13171-f302-7cd2-b535-705eeff2009b.
State remained readable after the final Core image update; doctor reports ok.

Core and authenticated NATS remain running. The native demonstration Worker is stopped after
verification, and the temporary integration broker is removed. Start the Worker as described in
README to execute further tasks. Next stage is Factory MCP; this is not the full MVP yet.
