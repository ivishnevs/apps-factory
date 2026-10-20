# Architecture — Foundation

Core is the sole owner of SQLite. A validated API request inserts a run and its command
outbox row in one transaction. The outbox publisher waits for a JetStream publish ack
before marking sent. A crash before marking may republish the same message ID.

Native Workers share a durable provider-specific pull consumer. Before runtime side effects,
a Worker atomically claims the run through Core; the command is only a pointer. Claim re-checks the
task against the database (trigger-log-scheduling D6), builds the brief in the same transaction and returns
a unique lease with the authoritative RunSpec, changes QUEUED to RUNNING, and enforces the agent concurrency
slot. A command for a run the task no longer wants (reassigned, cancelled, parked, a dependency reopened) is
answered `409 {"drop":true}`: the run ends `stale`, and the worker acknowledges the command without starting a
runtime, as it does for a duplicate command of a claimed run. A busy agent (`409 {"retry":true}`) leaves its
queued command pending for retry. Once claimed, broker command is acknowledged; lease recovery
handles a crash after this point. This deliberately does not promise exactly-once external effects.
A run still QUEUED after its command left the outbox gets the same command again (message ID `<run>/<n>`) after
1, 2, 4, 8 and then every 15 minutes since the last publish, and never while an earlier command is still
unpublished; a purged or restored stream therefore never strands work, and the duplicates are dropped by claim.
Each resend is a `run.command_redelivered` feed event. A task run nobody claims within `queue_ttl` (15 minutes)
ends `unclaimed` without an attempt and its task is dispatched again (`run.unclaimed`).

Worker heartbeats every 10s; leases expire after 45s. Heartbeats also deliver cancellation.
All Core time comes from `Store.Clock` (wall time unless a test injects `clock.Fake`). Core keeps an in-memory
continuity window that restarts on Core start or after a gap of more than 3 ticks (the host slept or the
process was paused); a lease expires only when both its `lease_until` and the window start plus one TTL have
passed, so after a wake every worker gets a full TTL to heartbeat before anything is declared lost.
Run statuses are QUEUED, CLAIMED (a v2 run claimed but not started), RUNNING, CANCELLING, then ended as COMPLETED,
FAILED or CANCELLED; the run's `outcome` says how it ended and alone decides recovery (reliable-recovery, see
[task lifecycle](task-lifecycle.md#failure-retry-and-cancellation)). Only Core decides `lost`: an expired lease, a missed start
window, a node restarted with a new epoch or an inventory without the run ends it FAILED `lost`. A lost run whose
runtime may still run (a silent v2 node) stays `unreleased` and keeps its workspace slot, session and task until
the node proves the runtime gone (a new epoch, which kills the old process groups first; a heartbeat without the
run; its stop) or the owner resolves it; nothing else of the project waits for it. Ordinary tasks then retry
automatically in the same workspace and session; tasks with external side effects are parked for inspection.
Queued cancellation is immediate, running cancellation is observed within a heartbeat interval.

SQLite has a unique writer index per workspace slot (`workspace_writer`) over active and unreleased lost runs.
A worktree task run holds its own slot (`<project workspace>#task/<task>`), so tasks of one project run in parallel
on nodes with worktrees; runs in the shared project workspace (shared or scratch mode, message runs, or a worktree
run claimed by a node without worktrees or by a v1 worker) hold the project workspace and run one at a time
(runtime-protocol-v2 §11, Worktrees). `active_task_run` keeps one live run per task and `session_turn` one per
session.

Workspace modes (agent `spec.workspace.mode`): `shared` and `scratch` runs use the project workspace through
`workspace.Allocator` (`workspace.Local`) with a canonical-path advisory flock.
Use one canonical host path per project: Core in Docker cannot resolve macOS symlink aliases.
Do not register the same physical directory under aliases, particularly when resolving a lost run.
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

Nodes (node-agent; details in [development](development.md#node-process)): runs execute on `factory-node run`, one
process per machine under launchd or systemd (`KeepAlive`/`Restart=always`), configured by `node.yaml`. It holds N
slots (goroutines running the shared executor `internal/worker`, each run in its own process group) over the
enabled runtime drivers (`internal/driver`: Codex app-server, Claude through the per-run Agent SDK shim); a driver
whose probe or version check fails, or whose run ended on an auth failure, stops claiming until a probe passes.
With [runtime protocol v2](runtime-protocol-v2.md) the node registers with a stable `node_id` and a new epoch on
every start, claims command pointers with `{node_id, epoch, claim_id, slot}` and renews every lease with one
inventory heartbeat; the answer carries Core's incarnation (a change makes the node stop, seal and re-register),
`stop`, `acked`, `cleanup`, `reset` and `pause`. The node authenticates with its own node token; its heartbeat also
carries the drivers' state (a driver becoming unusable is one `driver_unsupported` event, without a new epoch).
Core enforces declared `resources` (held until the run is sealed) and `labels` at claim. The
janitor runs `on_run_stop`/`on_task_closed` hooks, removes closed tasks' worktrees and prunes caches; its hooks and
the stack checks come from the company catalog's `NodeHooks` document, which Core delivers in the register and
heartbeat answers (node.yaml `janitor` overrides per list). The heartbeat's health (disk level, failing self-checks,
a stalled heartbeat) and the registers (restarts) become node health events in the feed
(`node.low_disk`, `node.disk_critical`, `node.restart_loop`, `node.check_failed`, `node.heartbeat_stalled` and
`node.offline` after 10 minutes carry attention; `node.registered`, `node.online`, `node.paused`/`resumed` do not),
one per episode; `factory status` lists the nodes. Command
guards put refusing or approval-checking shims first on each run's `PATH` (Claude also checks shell commands in a
`PreToolUse` hook; see [policies](policies.md#command-guards)); they are guardrails, not isolation. A disk gate
stops claiming below the critical watermark, and a watchdog reaps runtime groups that belong to no slot.
`factory-node doctor` runs the self-checks locally without Core.

Events carry a versioned envelope, event ID, correlation ID, causation ID and timestamp.
Worker events carry the Core lease. Core atomically persists an event and updates the run,
then acknowledges broker delivery. SQLite event IDs deduplicate replay. Progress never revives
terminal runs. Late terminal evidence replaces a Core-decided `lost` (or `timed_out`) end once.
If broker event publication fails, Worker stops execution. Missing terminal evidence expires to `lost`.

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
