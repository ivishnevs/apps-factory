# Task Engine

Core owns persistent projects, tasks, dependencies, attempts, comments, artifact references and
review decisions. Plans remain dynamic; there is no Manager -> Developer -> QA sequence in code.
A project has a unique ID, immutable canonical macOS workspace, name/description and version.
Project updates change name/description with an expected version. Workspace allocation remains
one active writer; use a single canonical path per physical directory (Core cannot resolve host aliases).

## Lifecycle

BACKLOG -> READY -> IN_PROGRESS -> IN_REVIEW -> DONE.
IN_REVIEW can reject to REWORK; explicit ready returns it to the scheduler.
BACKLOG/READY/REWORK/FAILED can be manually BLOCKED with a reason, then explicitly readied.
FAILED can retry within policy limits or via explicit ready. CANCELLED and DONE are final.
Execution with unknown outcome becomes BLOCKED and quarantines the run/workspace.

Tasks may also declare `required_artifacts` with exact id/version/URI. Unsatisfied owner
approvals produce an automatic artifact BLOCKED state; approval restores READY without
changing manual blocks or UNKNOWN quarantine. Dispatch and claim pin/check the approved inputs.

READY expresses permission to schedule; it does not assert prerequisites are satisfied.
All dependencies must be DONE. IN_REVIEW, failed, blocked and cancelled dependencies do not satisfy
this gate. Cross-project dependencies, duplicate/self edges and DAG cycles are rejected transactionally.
Dependencies/assignment/metadata can change before execution, after rejection or known failure.
They cannot change during active execution, review, final state, or an unresolved UNKNOWN run.
Parent tasks must exist in the same project; parent assignment is immutable and does not imply an
execution dependency. OpenSpec change is a stored reference, not an implemented OpenSpec adapter.
Cycles are checked on "waits for" edges — a task waits for its dependencies and a parent for its children —
so a dependency that is the task's parent or reaches it is refused with 409 `cycle`, like any DAG cycle.

Tasks created through Factory MCP record `origin_task` and `origin_run` (from the run credential, never from
tool arguments). Creation is deduplicated: when an open task with the same origin task, the same title
(case-folded, whitespace collapsed) and an assignee of the same role exists, Core returns it with
`deduplicated: true` instead of creating another. Owner-created tasks are never deduplicated.

Scheduler selects by descending priority, then creation time/ID. It atomically verifies the task
version, dependencies and slots, inserts the run and attempt, marks IN_PROGRESS and enqueues a
durable command. A unique active task-run index prevents duplicate dispatch. Queued/running runs
reserve the agent for scheduling; existing project/workspace/session indexes protect writers.
There is currently one active run per configured agent. Different projects/agents may run concurrently
using separate Worker processes. A task may be IN_PROGRESS while its run is still QUEUED for a Worker.

## Submission and review

A completed runtime turn implicitly submits the result to IN_REVIEW. It never marks DONE.
The owner or an authorized different agent reviews it. Named assignees cannot accept their own work.
Explicit submission in IN_REVIEW may attach summary, commit, declared checks and versioned artifact
references for the current run. Check statements are trusted per the agreed MVP specification.
Artifact references do not grant approval: see the separate [artifact approval/inbox workflow](artifact-approvals.md).
Reject requires feedback, persists a comment, sets REWORK and leaves execution paused until ready.
Rework resumes only this task's previous thread; other tasks start independent threads.

Each attempt persists RunSpec, task/attempt identifiers, provider, config hash/full resolved snapshot,
available skill names, session ID, timestamps, output and terminal status. Actual skill reads remain
observable runtime events; the skill list is the offered set, not a claim that every skill was used.
Historical migration-1 runs have zero start/finish timestamps because those were not recorded then.
Task events in SQLite preserve earlier submissions/review states even after rework. Broker entity
notifications carry bounded identity/status/version pointers; fetch snapshots/history through Core. Comments record review and
action reasons; artifact rows retain references per run/version.

## Failure, retry and cancellation

Only an explicit Worker FAILED turn is eligible for bounded retry: at most max_retries after the
initial attempt, using the previous run's configuration snapshot. Attempt count includes rework,
so the limit is conservative across the task's entire history. The demo profile has max_retries 0.
Retry context includes previous diagnostics and instructions to inspect existing changes and avoid
repeating completed side effects. No exception from starting/communicating with runtime is treated
as a known failure; Worker reports UNKNOWN conservatively.

UNKNOWN never auto-retries. Lease expiry and event handling update run/task in the same transaction.
After workspace inspection use runs resolve; COMPLETED submits for review, FAILED leaves an explicit
ready decision to the owner, CANCELLED ends the task. Reconciliation is audited and does not itself
allow automatic retry. Late trustworthy terminal evidence may resolve UNKNOWN before manual resolution.

Task cancellation cancels a queued run immediately or sets CANCELLING for heartbeat-driven runtime
interruption. Writer reservation remains until a known terminal result. UNKNOWN still requires
reconciliation even when cancellation was requested. Calling runs cancel for a task-linked run also
updates its task. A completion racing a cancellation leaves the task CANCELLED, not accepted.

## Versions and idempotency

Updates/actions/submit/review require the current task version; stale versions return 409.
Project update also requires version. Optional request_id records a payload fingerprint and the
original response in the mutation transaction. Same key/input replays that response; changed input
with the same key returns 409. A replay response can be older than current state: GET if needed.
CLI generates a fresh key per invocation unless a JSON mutation supplies request_id. CLI does not
blindly retry a failed HTTP request. Scheduler dispatch and Worker event replay are independently
idempotent. Invalid mutation rollback also rolls back dependency edits, events and outbox.

## CLI

```sh
mkdir -p .cache/my-project
./bin/factory projects create my-project "$PWD/.cache/my-project"
./bin/factory tasks create - <<'JSON'
{"id":"first-task","project_id":"my-project","title":"First task","description":"Reply TASK_ONE_OK; do not modify files.","assignee":"demo","request_id":"create-first-task"}
JSON
./bin/factory tasks create - <<'JSON'
{"id":"second-task","project_id":"my-project","title":"Second task","description":"Reply TASK_TWO_OK; do not modify files.","assignee":"demo","dependencies":["first-task"],"request_id":"create-second-task"}
JSON
./bin/factory tasks ready first-task 1
./bin/factory tasks ready second-task 1
./bin/factory tasks list my-project
./bin/factory tasks get first-task
# Once IN_REVIEW, use its current version from GET:
./bin/factory tasks review first-task VERSION accept
./bin/factory tasks attempts second-task
./bin/factory projects events my-project
```

Other commands: tasks update/submit ID JSON_FILE (or - for stdin), tasks block/cancel ID VERSION
[REASON], tasks review ID VERSION reject FEEDBACK, tasks events/comments ID,
projects get/list/events and projects update ID JSON_FILE. IDs are globally unique; use fresh IDs for
another demonstration. A native Worker must be running to execute scheduled runs.

## HTTP and authorization

POST/GET /projects; GET/PATCH /projects/{id}; GET /projects/{id}/events.
POST/GET /tasks; GET/PATCH /tasks/{id}; GET /tasks/{id}/attempts/events/comments;
POST /tasks/{id}/actions, /submit, /review. GET /tasks accepts project/status filters.
JSON contracts are defined in internal/tasks/types.go. Creation starts in BACKLOG.
Actions take {version,action,reason?,request_id?,actor?}; submit takes {version,run_id,submission,...};
review takes {version,accept,feedback?,...}. Clients cannot set arbitrary status.

All endpoints use the existing trusted owner/service token. Empty actor means owner authority.
Named task operations additionally evaluate factory.task.* policies and creation/assignment
communication permissions. Named submitters must be assignees, named reviewers cannot be assignees.
Denial returns 403; approval-required returns 423; no boolean approval bypass exists. Task creation
validates assignee configuration. Actor labels are caller-supplied metadata under the shared token,
not authenticated agent identity. Factory MCP and scoped agent credentials remain later work.

Operational limits: title <= 1 KiB, description <= 64 KiB, priority -1000..1000, <=128 dependencies,
and serialized run context <=900 KiB. Dependency/previous-result excerpts are bounded; full results
remain readable through task/attempt APIs. Oversized generated context blocks the task with a reason
rather than stranding an unpublishable broker command. Event feeds are not paginated yet.


Factory MCP extension: structured submit also accepts IN_PROGRESS for the current run, saving
only a draft. It preserves the writer lock; known runtime completion alone enables IN_REVIEW.
A successful completed run preserves an explicit draft instead of replacing it with final text.
See factory-mcp.md for run credentials, agent review and the audit endpoint. The legacy owner API
still permits caller-supplied actor labels; MCP derives identity exclusively from credentials.


Messaging extension: wait_message_id identifies a blocking question asked during the active run.
A successful turn with that marker becomes BLOCKED while unanswered or READY if already answered,
instead of IN_REVIEW. A later explicit answer resumes only a message-blocked task with a known
COMPLETED prior run. Dispatch resumes the same task thread, adds the answer and clears the marker.
No submission is accepted while waiting. Cancellation, failure, unknown outcomes and manual blocks
require their existing explicit handling; an answer does not override them. See messaging.md.
