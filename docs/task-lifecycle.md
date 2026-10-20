# Task Engine

Core owns persistent projects, tasks, dependencies, attempts, comments, artifact references and
review decisions. Plans remain dynamic; there is no Manager -> Developer -> QA sequence in code.
A project has a unique ID, immutable canonical macOS workspace, name/description and version.
Project updates change name/description with an expected version. Use a single canonical path per physical
directory (Core cannot resolve host aliases).

Runs hold writer slots, not the project (task-worktrees D1). A run in the project workspace (agent
`spec.workspace.mode` shared or scratch, a project without `repo_url`, message runs, or a node without
`worktrees`) holds the project workspace path, so such runs of one project go one at a time. A task run in mode
`worktree` (its own branch `af/<task>…`) or `fresh_from_default` (reset to the freshly fetched default branch
at every run, no task branch) on a node with worktrees holds its own slot `<workspace>#task/<task>` and runs in
that task's worktree on the node, so tasks of one project run in parallel; the node serializes runs of one task
on its worktree (flock, process group, seal onto `refs/factory/wip/<task>`). Unpushed work of a `worktree` task
pins it to its node; a `fresh_from_default` worktree never pins.

## Lifecycle

BACKLOG -> READY -> IN_PROGRESS -> IN_REVIEW -> DONE.
IN_REVIEW can reject to REWORK; explicit ready returns it to the scheduler.
BACKLOG/READY/REWORK/FAILED can be manually BLOCKED with a reason, then explicitly readied.
FAILED can retry within policy limits or via explicit ready. CANCELLED and DONE are final.
Execution with unknown outcome becomes BLOCKED and quarantines the run/workspace.

Tasks may also declare `required_artifacts` with exact id/version/URI. Unsatisfied owner
approvals hold the task: it stays READY with reason `artifacts`, and reads present it as BLOCKED with an
`artifact:` block reason (not stored; migration 0028 converted stored ones). Approval releases it without
changing manual blocks or UNKNOWN quarantine. Dispatch and claim pin/check the approved inputs; a revoked
approval ends a still QUEUED run `stale`.

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

Scheduler dispatches READY tasks whose `why` verdict is `runnable`, by descending priority, then the oldest
unhandled trigger (the work waiting longest), then ID (trigger-log-scheduling D4). It atomically verifies the
task version, dependencies and slots, inserts the run and attempt, marks IN_PROGRESS and enqueues a
durable command; the run's prompt is built later, at claim. A unique active task-run index prevents duplicate dispatch. Queued/running runs
hold their writer slot (the `workspace_writer` index over the run's slot above) and their session
(`session_turn`); an agent runs at most `max_concurrent_runs` (and `max_concurrent_runs_per_project`) runs at
once. Different tasks, projects and agents run concurrently on node slots. A task may be IN_PROGRESS while its
run is still QUEUED for a node.

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

Automatic retry (reliable-recovery D2–D3; owner decision 2026-10-14: autonomy first, dangerous or wasteful
cases are handled when they appear). When a task's run ends, the outcome table (`storage.Outcomes`, see Run
outcomes) decides in one place (`recoverTask`, after `syncTaskRun`):

- `failed`, `timed_out`, `idle_timeout`, `max_turns`, `setup_failed` count as an attempt
  (`attempts_no_progress`), `lost` does not; all of them apply the backoff: `fail_streak` + 1 and
  `not_before` = now + 1, 5, then 15 minutes. The task goes back to READY and the scheduler dispatches it
  after `not_before`, in the same workspace and session (the node's ResumePolicy may start a fresh one), with
  "Previous run: <outcome> (automatic retry)" and the previous result in the prompt. Its triggers are not
  consumed, so the retry handles the same work.
- `paused_out` (quota, auth, missing runtime) counts nothing: the task is READY again and runs when the pause
  lifts. `revoked` and `stale` keep today's behaviour (cancel → CANCELLED, kill switch → READY).
- `succeeded` resets the counters and the backoff.
- A failed merge is an ordinary attempt (task-worktrees D7): the implementer of an approved task whose merge
  failed (checks, branch protection, conflicts) exits `factory_exit_handoff` to itself with `merge_failed`
  (why). It counts an attempt with backoff (1, 5, 15 minutes) and the task runs again after `not_before`; the
  third failed merge in a row without progress leaves the task FAILED with a `retries_exhausted` escalation.
  `merge_failed` before an approval, by another agent or to another agent is refused `invalid_exit`.
- Unmerged branches: once a day (and at Core start) Core lists tasks closed (DONE or CANCELLED) in the last 30
  days whose last exit facts show their branch ahead of the default branch with a pull request not merged and
  no `abandon_branch` answer; each gets exactly one `task.unmerged_branch` feed event (attention) addressed to
  the manager of its last assignee (`agent`), naming the PR and `factory abandon-branch TASK`.
- A task's `model` override (runtime-drivers D1, agent-configuration.md) reaches the run's `runtime_options`
  when it fits the assignee's provider; otherwise the run uses the agent's model and records
  `task.model_ignored`.
- Budget: every budget outcome (all of the above except `revoked`, `paused_out`, `stale`, and losses caused by
  a node restart) counts in `task_budget` per task and UTC day.
- Three attempts in a row without progress open a `retries_exhausted` escalation, 30 budget runs in a day a
  `budget_exhausted` escalation; the task stays FAILED. Answers `retry` (counters, backoff and today's budget
  reset, the task runs now) or `cancel`. Releasing the task (`factory tasks ready`) has the same effect as
  `retry` and clears the escalation; reassigning it resets the counters for the new assignee.
- Lost runs: a v1 run whose lease expired is released at once (the worker stops a run whose heartbeat fails).
  A v2 run lost to a new node epoch, or missing from the node's inventory, is released too. A v2 run lost
  because the node went silent (or stopped past its timeout) may still be running: it keeps its writer slot
  (the project workspace or its task worktree) and its session, and its task stays BLOCKED (`parked:inspect`) until the node proves it
  gone — it registers a newer epoch, sends a heartbeat without it, or reports the stop of a run Core declared
  lost (that CANCELLED is a confirmation, not an owner cancel). Then the task retries as above. Only an
  unreleased UNKNOWN run holds its writer slot.
- `side_effects: true` (task create/update JSON) marks a task whose runs act outside the workspace (publish,
  deploy): it is never retried automatically. A failure leaves it FAILED for the owner; an unknown outcome
  parks it for inspection (`runs resolve`, required even when the runtime is known to be gone).
  `max_retries` in agent YAML still applies only to these tasks.

Late trustworthy terminal evidence still resolves an UNKNOWN run. Reconciliation (`runs resolve`) is audited:
COMPLETED submits for review, FAILED leaves the decision to the owner, CANCELLED ends the task.

### Run outcomes

Every ended run records one outcome next to its status (`outcome` in run reads and task attempts,
`STATUS/outcome` in `factory show`), from the reliable-recovery table: `succeeded` (COMPLETED), `failed`,
`setup_failed` (OpenSpec preflight, skills or `workspace_diverged` before the runtime started),
`timed_out`, `idle_timeout`, `max_turns`, `paused_out` (reported by the worker), `lost` (lease, start
window or node lost; UNKNOWN and FAILED `lost: not started`), `revoked` (cancel, kill switch) and `stale`
(a queued run cancelled by a claim-time check). The `run_outcome` trigger derives it unless the code that
ended the run set one; late terminal evidence on an UNKNOWN run replaces `lost`, an owner's
`runs resolve` does not. The attempt/backoff/budget flags of each outcome are `storage.Outcomes` and drive the automatic retry
above.

The hang detector stops a started run that has had no event for 30 minutes (see runtime-protocol.md, Idle
stop): it ends FAILED `idle_timeout`, a known failure, and its task is not cancelled. A worker's own
timeout reported as `outcome: timed_out` is handled the same way.

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
[REASON], tasks review ID VERSION reject FEEDBACK, tasks model ID VERSION MODEL|- (the task's model override,
authority only), tasks events/comments ID,
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
only a draft. It keeps the run's writer slot; known runtime completion alone enables IN_REVIEW.
A successful completed run preserves an explicit draft instead of replacing it with final text.
See factory-mcp.md for run credentials, agent review and the audit endpoint. The legacy owner API
still permits caller-supplied actor labels; MCP derives identity exclusively from credentials.


Messaging extension: a blocking question asked during the active run is an `ask` park on the task that names
the asking run; reads show it as `wait_message_id` while it is open. A successful turn of that run becomes
READY instead of IN_REVIEW: held by the open park (presented as BLOCKED, `message: waiting for answer`), or
runnable if the question was already answered. The answer clears the park and adds an `answer` trigger; the
next run resumes the same task thread and its brief carries the answer. No submission is accepted while
waiting. Cancellation, failure, unknown outcomes and manual blocks
require their existing explicit handling; an answer does not override them. See messaging.md.

## Why a task does not run

`factory tasks why ID` and `factory tasks why --project PROJECT` (add `--json` for JSON; HTTP
`GET /tasks/{id}/why`, `GET /projects/{id}/why`) print, for each non-terminal task, the first reason it does
not run now and who clears it. `tasks.Evaluate` is a pure function of a snapshot that Store loads in one
transaction; the scheduler dispatches only a `runnable` verdict and claim re-evaluates it
(trigger-log-scheduling D3).
`GET /tasks` and `GET /tasks/{id}` (and the Factory MCP task tools built on them) return every task with its
`reason` and `clearer`.

Order: `terminal`, `backlog`, `unassigned` (no assignee or not a configured agent), `live_run`, parks
(`parked:escalation` — failed with retries exhausted or not allowed; `parked:inspect` — current run UNKNOWN;
`parked:ask` — unanswered blocking question; `parked:block` — manual or OpenSpec block), `dependencies`,
`artifacts`, `backoff` (failed, automatic retry pending), `idle` (IN_REVIEW, REWORK, a READY task without an
unhandled trigger, and other states that wait for an explicit action), `capacity` (the agent is busy on another
task, or another task's run — including an UNKNOWN one — holds the project workspace's writer slot; runs in
other tasks' worktrees never do), else `runnable`. The verdict also
lists the pending triggers the next run will be briefed on.

## Trigger log and parks

Migration 0010 adds `task_triggers` and `task_parks` (trigger-log-scheduling D1–D2). Every cause writes its
trigger or park in its own transaction, and each appears in the feed (`task.trigger.<kind>`,
`task.parked.<kind>`, `task.unparked.<kind>`). Questions and artifact waits are no longer stored as status
BLOCKED: the `ask` park and the derived `artifacts` reason hold a READY task, and reads (task GET/list, MCP
task tools, `GET /tasks?status=BLOCKED`) present it as BLOCKED with `block_reason` and its `reason`. Manual,
OpenSpec, children and UNKNOWN-run blocks are still stored.

- Triggers raise `tasks.wake_seq`: `ready` (ready action; authority when the owner acts), `created_ready`
  (`POST /tasks` with `ready: true`; an agent needs `factory.task.ready`), `assignment` (assignee changed),
  `owner_comment` (`POST /tasks/{id}/comments`, `factory tasks comment ID TEXT`, by the owner with authority or
  by another agent; the assignee's own comment never wakes its task), `review_result` (rejection), `answer`
  (a blocking question answered), `dependency_done` (once, when the last open dependency becomes DONE),
  `last_child_closed` (once, when the last open child becomes DONE or CANCELLED by any path) and
  `artifact_approved`.
- Claim records the run's brief window `brief_from = handled_seq`, `brief_to = wake_seq` and builds the brief
  in the same transaction; a COMPLETED run sets `handled_seq = brief_to` (explicit exits replace this later).
- Claim re-checks the task (D6): a run that is no longer the task's current run, whose task was reassigned or
  cancelled, or whose `why` verdict (with the run itself ignored) is `terminal`, `backlog`, `unassigned`,
  `parked:*` or `dependencies` ends `stale` (no attempt, no backoff; feed `run.stale`) and the claim answers
  409 `{"drop": true}`; pauses, capacity and node reasons answer `{"retry": true}` and the run stays QUEUED.
  Reassigning a task whose run is still QUEUED ends that run `stale` at once and returns the task to READY.
- Reconciler (D5): a QUEUED run's command is resent after 1, 2, 4, 8, then 15 minutes (`<run>/<n>`), so a purged
  or restored command stream never strands work; a task run QUEUED for longer than `queue_ttl` (15 minutes)
  ends `unclaimed` (no attempt, no backoff; feed `run.unclaimed`) and its task is dispatched again.
- The brief (D8), built at claim: the task and its OpenSpec change; the previous run's outcome (every run after
  the first; with its last result when it did not succeed); directives and the triggers of the brief window
  with author, authority and their addressee when it was another agent (`task_triggers.addressee`, the
  assignee when the trigger was written; migration 0028); answers to blocking questions; review feedback; comments since the previous run;
  tasks created by earlier runs with their status; the branch log from the facts of the task's last exit
  (branch, commits ahead of the default branch, head, push and pull request state); dependencies with their
  results; approved artifact inputs; company and project rules; the review or exit instruction.
  `internal/storage/testdata/brief.golden` pins its format.
- Parks: `ask` (blocking question, cleared by its answer), `block` (manual or OpenSpec block, cleared by ready),
  `inspect` (current run UNKNOWN, cleared when the run is resolved or terminal evidence arrives). Cancel and DONE
  override all active parks. `escalation` is still derived from FAILED until reliable-recovery.
- The migration backfills a `ready` trigger for READY tasks and parks for existing manual/OpenSpec blocks,
  unanswered blocking questions and UNKNOWN current runs.

## Owner console v0

- `factory status [PROJECT]` (`GET /status`): per project the task counts by status, active runs (including
  UNKNOWN), how many tasks are runnable, every waiting task with its `why` reason and who clears it, and the
  inbox size.
- `factory watch [PROJECT] [--attention]` (`GET /feed`): the last 20 feed events, then new ones every 2 s; `!`
  marks events that need the owner.
- `factory log TASK` (`GET /feed?task=ID`): the whole feed history of one task.
- `factory show TASK`: the task, its `why` with pending triggers, dependencies, attempts, comments and the last
  10 feed events.

Every console command takes `--json` (`watch --json` prints one JSON object per event).

`GET /feed` filters by `project`, `task` and `attention=1` and pages with `after`/`limit`. Attention is set for
approval requests, questions to the owner and escalated messages, reports that need owner action, `ask` parks
answered by the owner, `inspect` parks and OpenSpec blocks; fuller rules (digest, Telegram) arrive with
waits-events-cost-cli.

## Stall detection and escalations

Once a minute Core's tick checks every released, non-terminal task (reliable-recovery D8):

- `stalled` — the task depends on a task that is in BACKLOG, has no assignee, or was cancelled (a cancelled
  dependency never becomes DONE). This opens an **escalation** (below) per (task, dependency), answers
  `unblock` (stop waiting on that dependency) or `cancel` (cancel the waiting task); it clears itself when the
  dependency is assigned and released, or closes.
- `idle` — the task has an assignee and its `why` reason has been `idle` for 24 hours: one attention feed event
  `task.alert.idle` per episode (`task_alerts`, migration 0012), cleared with `task.alert_cleared.idle`.

An escalation (reliable-recovery D5, migration 0017) is a park of kind `escalation` on the task (`why` shows
`parked:escalation` and who answers) plus a question from `factory` with the allowed answers as one
`single_choice` field. Its target is, until reporting chains exist, the configured agent with role `manager`
that has the fewest open escalations (then by name), else the owner. A manager's question goes to it as an
ordinary message run; unanswered for 24 hours it escalates to the owner, who can always answer. At most one
escalation is open per (task, reason, ref). Answering applies the answer, clears the park and adds an `answer`
trigger (authority when the owner answers); an invalid option is refused, and `cancel` on a task with open
children is refused with `needs_cascade` (the escalation stays open). A condition that goes away, or the task
closing, clears the escalation and withdraws its question (status `WITHDRAWN`). Feed: attention events
`task.escalation.<reason>`, then `task.escalation_answered.<reason>` or `task.escalation_cleared.<reason>`.

- `factory escalations [--all]` (`GET /escalations[?all=1]`) lists open (or all) escalations with their
  question id, target and answers.
- `factory answer MSG OPTION` answers an escalation question with one of its options (any single-choice
  question); other structured forms still use `factory inbox answer MSG JSON_FILE`.

Retry escalations (`retries_exhausted`, `budget_exhausted`) are described under Failure, retry and
cancellation.

## Owner controls

- `factory pause --all | project ID | agent NAME [REASON]`, `factory resume …`, `factory pauses`
  (`POST /pauses`, `POST /pauses/resume`, `GET /pauses`; migration 0013): no new task or message run is
  dispatched in a paused scope; live runs continue. `why` shows `paused:company|project:<id>|agent:<name>`
  (after `artifacts`, before `backoff`) with the resume command. Pauses and resumes are feed events
  (`owner.paused` with attention, `owner.resumed`).
- Quota pauses: a worker event with `quota` pauses the agent's quota key (`spec.runtime.quota_key`, default the
  agent name; every agent sharing the key) until `resets_at` (unknown: 1 hour); a later reset extends it, an
  earlier one never shortens it. The tick ends expired pauses. `why` shows `paused:quota_key:<key>` cleared by
  `time`; `factory resume quota_key KEY` ends it early. Event `quota.paused` carries attention. Agents on other
  keys keep running. A new pause opens one `quota_paused` escalation whose scope is the key (no task, no task
  triggers; migration 0027 makes `escalations.task_id` nullable): a question to the owner with answers `wait`
  (claims resume at the reset) and `resume` (lift the pause now). It clears itself, withdrawing the question,
  when the pause ends (reset reached or `factory resume quota_key KEY`). Declared keys:
  `configs/quota_keys.yaml`.
- `factory stop [REASON]` (`POST /stop`) is the kill switch: it pauses the company and revokes every live run.
  A QUEUED run ends at once; a RUNNING run is asked to stop through its heartbeat. A revoked run that ends
  CANCELLED returns its task to READY instead of cancelling it (a turn that completes first is submitted as
  usual; a task the owner cancelled stays cancelled). `factory start` resumes the company.
- `factory rules company|PROJECT [TEXT|-]` shows or replaces standing rules (conventions, facts, fixed decisions;
  `GET|PUT /settings/company`, project `rules` in `PATCH /projects/{id}`, up to 8 KiB each). Every brief carries
  them; agents reread them with `factory_settings_get`.
- `factory pending` (`GET /pending/count`): inbox items, pending approvals, active stall alerts, runs to inspect,
  tasks in review, and their total.
- `factory answer MESSAGE_ID TEXT` answers a question without a structured form; `factory approve|reject
  APPROVAL_ID REASON` decides an approval. Structured forms still use `inbox answer ID JSON_FILE`.
- `GET /feed/stream` streams the feed as server-sent events (`id` = feed sequence; filters `project`, `task`,
  `attention`); a client reconnecting with `Last-Event-ID` or `?after=` receives every later row once. The
  stream is exempt from the server's write timeout.
