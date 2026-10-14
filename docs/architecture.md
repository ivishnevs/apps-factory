# Architecture — Foundation

Core is the sole owner of SQLite. A validated API request inserts a run and its command
outbox row in one transaction. The outbox publisher waits for a JetStream publish ack
before marking sent. A crash before marking may republish the same message ID.

Native Workers share a durable provider-specific pull consumer. Before runtime side effects,
a Worker fetches authoritative RunSpec and atomically claims it through Core. Claim returns
a unique lease, changes QUEUED to RUNNING, and enforces the agent concurrency slot. A duplicate
command for a claimed run is acknowledged without executing again. A busy agent leaves its
queued command pending for retry. Once claimed, broker command is acknowledged; lease recovery
handles a crash after this point. This deliberately does not promise exactly-once external effects.
A run still QUEUED after its command left the outbox gets the same command again (message ID `<run>/<n>`) after
1, 2, 4, 8 and then every 15 minutes since the last publish, and never while an earlier command is still
unpublished; a purged or restored stream therefore never strands work, and the duplicates are dropped by claim.
Each resend is a `run.command_redelivered` feed event.

Worker heartbeats every 10s; leases expire after 45s. Heartbeats also deliver cancellation.
All Core time comes from `Store.Clock` (wall time unless a test injects `clock.Fake`). Core keeps an in-memory
continuity window that restarts on Core start or after a gap of more than 3 ticks (the host slept or the
process was paused); a lease expires only when both its `lease_until` and the window start plus one TTL have
passed, so after a wake every worker gets a full TTL to heartbeat before anything becomes UNKNOWN.
Core marks expired active runs UNKNOWN. UNKNOWN releases the agent slot, but quarantines the
project, workspace and known session. Owner reconciliation is explicit; no automatic runtime retry.
Queued cancellation is immediate, running cancellation is observed within a heartbeat interval.

SQLite has a unique writer index per workspace slot (`workspace_writer`) over active and unreleased UNKNOWN runs.
A worktree task run holds its own slot (`<project workspace>#task/<task>`), so tasks of one project run in parallel
on nodes with worktrees; runs in the shared project workspace (shared or scratch mode, message runs, or a worktree
run claimed by a node without worktrees or by a v1 worker) hold the project workspace and run one at a time
(runtime-protocol-v2 §11, Worktrees). `active_task_run` keeps one live run per task and `session_turn` one per
session.

Workspace modes (agent `spec.workspace.mode`): `shared` and `scratch` runs use the project workspace through
`workspace.Allocator` (`workspace.Local`) with a canonical-path advisory flock.
Use one canonical host path per project: Core in Docker cannot resolve macOS symlink aliases.
Do not register the same physical directory under aliases, particularly when reconciling UNKNOWN.
The `.factory-writer.lock` file may remain after release; flock state, not file existence, owns the lock.

`worktree` runs (node `worktrees.enabled`; runs with a `task_id` and the project's `repo_url`) get one git worktree
per task, managed by the node (`internal/workspace.Worktrees`): a shared clone per project, the task's worktree on
branch `af/<task>` and a scratch directory (`FACTORY_SCRATCH`) outside it. Each worktree has its own lock and run
pidfile, so a second run of the same task waits, and later runs of the task continue in the same worktree.
- Seal: at run end the runtime's process group is stopped, then uncommitted work is committed onto the local ref
  `refs/factory/wip/<task>` without touching HEAD, index or files, and `sealed_head` is recorded. Nothing is pushed
  by the seal. After an accepted handoff, submit or done the seal then resets the worktree to the exit's commit
  (`exit_sha`), so the next run starts exactly from what was handed over; after other exits it continues as is. The
  node reports `{mode, branch, node_id, exit_sha, sealed_head, sealed_at}` to `POST /runs/{id}/workspace`
  (one `workspaces` row per task, `GET /tasks/{id}/workspace`). A worktree left locked by a crashed process is
  sealed as that run's work before the next run, or at node start.
- Start check (under the lock, before the runtime starts): HEAD at `sealed_head` starts the run, after a
  fast-forward when `origin/<branch>` is strictly ahead; a HEAD changed outside any run, or uncommitted changes that
  block the fast-forward, end the run FAILED with `workspace_diverged` before the runtime starts and Core opens the
  `workspace_diverged` escalation (answered by a reset); a missing worktree is recreated from the local, else remote, else default branch.
- Reset: `factory-node workspace reset -task ID` (node side of reset-workspace) seals, removes and recreates the
  worktree from the remote or default branch, keeps the abandoned local state on `refs/factory/reset/<task>` and
  forgets the task's sessions so the next run starts fresh. Core sends the same reset to the node in the v2
  heartbeat answer (escalation answers, `pinned_node_unavailable`).
- Janitor (node): for each task in Core's `cleanup` list (v2 heartbeat answer) it takes the worktree lock, seals,
  runs the `on_task_closed` hooks and removes the worktree and its scratch directory; the branch, the WIP ref and
  the clone stay. `on_run_stop` hooks run after every run, `prune` entries periodically and at low disk.
- Pushing the task branch and opening its pull request happen only through explicit exits (`factory_exit_*`, behind
  `FACTORY_EXPLICIT_EXITS`); see [explicit exits](explicit-exits-contract.md). The implementing agent merges the
  approved pull request itself (`gh pr merge`, skill `task-pull-request`). Tasks with unpushed work are pinned to
  their node at claim.

Events carry a versioned envelope, event ID, correlation ID, causation ID and timestamp.
Worker events carry the Core lease. Core atomically persists an event and updates the run,
then acknowledges broker delivery. SQLite event IDs deduplicate replay. Progress never revives
terminal runs. Late terminal evidence may resolve UNKNOWN unless the owner already reconciled it.
If broker event publication fails, Worker stops execution. Missing terminal evidence expires to UNKNOWN.

Streams use file storage; their retention is currently unbounded. Configure operational backups
and retention before production use. Shared tokens/passwords protect trusted local components;
there is no per-agent authorization or strict OS isolation in Foundation. Full host access is
intentional. Prompt instructions for protected actions are not an enforcement boundary.

Agent Config extends this with resolved YAML profiles, a shared skill registry and one policy
catalog for access/escalation/reporting. Core enforces the resolved evaluator on scoped skill
operations. Worker receives composed policy instructions and skill descriptions, and supplies a
native Factory MCP adapter for lazy reads and task operations using run credentials. No new RPC method,
model loop or second owner of state is added. See agent-configuration.md, policies.md
and skills.md for precedence and the distinction between structured checks and trusted-agent behavior.


Task Engine adds transactional schema migration 2, registered projects and dependency DAG tasks.
Scheduler dispatch atomically creates the attempt/run/outbox and reserves the task. Worker terminal
events project to task submission/failure/blocked state in the same SQLite transaction as deduplication.
Review remains explicit and only DONE dependencies unblock successors. See task-lifecycle.md.
Full entity snapshots stay in SQLite audit events; bounded JetStream notifications carry entity,
project, status/version pointers. Subscribers fetch current state or audit history through Core.


Factory MCP adds migration 3 (tool call audit), a native stdio adapter and a run-scoped Core
gateway. Worker claims a run then obtains a lease-bound credential; the runtime receives this
credential instead of owner/broker tokens. Core checks captured/current policies, communication
permissions and project ownership before forwarding to domain operations. Audits persist across
restart. Structured submission remains a draft until successful terminal runtime evidence.
All scheduling and durable state ownership remain in Core. See factory-mcp.md.


Messaging adds migration 4, durable conversations/messages and separate message delivery runs.
Core schedules communications with the existing outbox/leases and writer constraints. Explicit
blocking questions persist a task wait marker; only known successful runtime completion releases
the writer and waits or schedules same-thread continuation after an answer. Conversation IDs
correlate message events. Owner dialogs persist their own completed thread; task responders use
separate sessions. Core resumes work and manages deadlines/escalations; Worker has no messaging loop.
See messaging.md for permissions, owner inbox and failure semantics.

Action approvals persist exact scoped calls and owner decisions in SQLite v5, link to inbox and tool audit, and resume tasks through the existing messaging wait lifecycle. See [approvals](approvals.md).

SQLite v6 adds immutable project artifact versions and typed owner artifact decisions. Task required_artifacts gates dispatch and claim; RunSpec retains the approved input versions. Task review, content approval and action authorization stay independent. See [artifact approvals](artifact-approvals.md).

SQLite v7 adds immutable significant reports, observed runtime outcomes, versioned decisions with one approved choice per project/topic, explicit replacement and bounded approved context in new RunSpecs. Exact owner action grants protect agent decision recording. Project feed exposes short event pointers; full snapshots remain in entity events. See [reports and decisions](reports-decisions.md).

OpenSpec integration maps a host workspace root into Docker Core, exposes confined lazy file reads and checks active task links at create/update/ready/dispatch/claim. Native Worker performs a final preflight. Initialization stores only directory metadata; specification bodies remain in files. See [OpenSpec](openspec.md).
