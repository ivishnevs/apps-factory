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

Worker heartbeats every 10s; leases expire after 45s. Heartbeats also deliver cancellation.
All Core time comes from `Store.Clock` (wall time unless a test injects `clock.Fake`). Core keeps an in-memory
continuity window that restarts on Core start or after a gap of more than 3 ticks (the host slept or the
process was paused); a lease expires only when both its `lease_until` and the window start plus one TTL have
passed, so after a wake every worker gets a full TTL to heartbeat before anything becomes UNKNOWN.
Core marks expired active runs UNKNOWN. UNKNOWN releases the agent slot, but quarantines the
project, workspace and known session. Owner reconciliation is explicit; no automatic runtime retry.
Queued cancellation is immediate, running cancellation is observed within a heartbeat interval.

SQLite has a unique writer index per project and normalized workspace string. Worker implements
a workspace.Allocator interface with a canonical-path advisory flock, allowing future worktrees.
Use one canonical host path per project: Core in Docker cannot resolve macOS symlink aliases.
Do not register the same physical directory under aliases, particularly when reconciling UNKNOWN.
The `.factory-writer.lock` file may remain after release; flock state, not file existence, owns the lock.

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
