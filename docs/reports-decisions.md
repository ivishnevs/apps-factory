# Reports and Decision Registry

Core stores significant reports and versioned project decisions in SQLite schema 7. No additional
agent runtime loop is introduced. The same APIs work for developer, manager, designer and QA roles;
access still depends on each agent's captured and current tools/policies/communication permissions.

## Reports

`factory_report_create` records `kind`, `summary` (what happened), `purpose`, `changes` (files,
dependencies or settings), `result`, optional exact registered `artifacts`, `needs_owner_action`
and `owner_action`. Supported agent kinds: dependency_installation, environment_change,
plan_deviation, problem, stage_completed. The engineering reporting policy selects significant
events; ordinary progress does not need a separate report. Identity, project, task and run are
injected by the gateway. Reports are immutable, request_id makes retry idempotent, and different
arguments with the same key conflict. Input is bounded to 32 KiB.

A needs_owner_action report creates a durable owner review_request notification with report_id.
It requires owner communication permission; it neither blocks a task nor grants approval. A real
blocking question still uses the messaging tools. Proposal notifications similarly carry decision_id.
Reading an inbox notification acknowledges it without approving anything.

Core automatically records observed COMPLETED/FAILED/CANCELLED/UNKNOWN run outcomes as source=core,
kind=runtime, once per run/status. This includes queued cancellations and lease expiration. UNKNOWN
also creates an owner reconciliation notice; resolving it closes that notice and records the observed
terminal outcome. COMPLETED describes runtime completion, not task acceptance. Core does not infer
successful dependency installation from shell output. Legacy direct-run projects without a project
record receive runtime reports but cannot receive project inbox notifications. There is no retroactive
backfill of old terminal runs.

## Decisions

A proposal contains topic, value, rationale and optional registered artifacts; task/run/creator are
injected for MCP calls. Topics are case/whitespace normalized within the project. A proposal creates
an owner review notification without automatically blocking work. Statuses: proposed,
under_discussion, approved, superseded, rejected. Ordinary engineering choices remain autonomous;
this registry is for significant project choices that should be remembered. Owner-reviewed proposals and policy-delegated decisions are distinct paths.

The authenticated owner records under_discussion/approved/rejected with expected version,
rationale and stable request_id. An agent calling factory_decision_record needs an APPROVED action
grant for the exact call, including target ID, version, status, rationale, request_id and replacement
fields. The mandatory system policy protects this operation; a grant cannot override deny rules.
For this owner-review path, approved_by is the owner; recorded_by identifies who executed the record operation, and an
agent operation also links its action approval. Owner HTTP endpoints reject run tokens. Chat answers,
report notifications, task review and artifact approval cannot approve a registry decision.

There is one approved decision per project/topic. To replace it, propose a new immutable value and
approve it with supersedes_id and supersedes_version matching the current approved record. Core
supersedes the old record and approves the new record in one transaction. A stale version, missing
replacement or concurrent loser returns 409. Previous values, rationales and linked full snapshots
remain in project entity events. An approved value cannot be edited in place.

Every new task, direct or message run receives approved decisions captured in its creation
transaction: DecisionContext / JSON decision_context and a prompt excerpt. At most 32 entries,
2048 bytes of value and 1024 bytes of rationale per entry; truncation and more_decisions are explicit.
Existing run snapshots retain their original context after replacement. Use decision_list/get for
current/full records, especially before repeating an owner question. Decisions are context, never
permission grants. Runtime-neutral snapshots support later agent/runtime additions.

## API and CLI

Owner endpoints use FACTORY_TOKEN:

- POST/GET /projects/{project}/reports; GET /reports/{id}.
- POST/GET /projects/{project}/decisions; GET /decisions/{id}.
- POST /decisions/{id}/record.
- GET /projects/{project}/context; GET /projects/{project}/feed.

Lists return items, next_after, has_more. after is a stable SQLite row/event sequence; default limit
20, maximum 100. Report filters: kind/task_id/run_id; decision filters: status/topic (owner list
without status includes all statuses). Feed items are short event pointers; full immutable snapshots
remain available through /projects/{project}/events. Status-filtered lists reflect current state and
can change while paginating.

```sh
factory reports create PROJECT report.json
factory reports list PROJECT 'kind=stage_completed&limit=20&after=0'
factory reports get REPORT_ID
factory decisions propose PROJECT proposal.json
factory decisions list PROJECT 'status=approved&topic=storage+choice'
factory decisions get DECISION_ID
factory decisions record DECISION_ID record.json
factory projects context PROJECT
factory projects feed PROJECT 'limit=20&after=0'
```

JSON files can be `-` for stdin. CLI supplies request_id if absent. record.json example:

```json
{"version":1,"status":"approved","rationale":"Reviewed requirements","request_id":"review-1"}
```

MCP: factory_report_create/get/list, factory_decision_propose/get/list/record,
factory_project_context/feed. List default limit 10, maximum 20; short metadata/excerpts avoid large
responses. MCP decision_list defaults to approved; explicit status="" requests all. Full get reads
one bounded record. MCP project context includes at most 12 decision excerpts and bounds project
text, with explicit truncation flags; fetch full project/decision separately when needed.

## Verification

`make check` covers immutable/idempotent reports, owner notices without blocking, automatic outcome
reports and UNKNOWN reconciliation, project/identity/communication boundaries, exact agent grants,
concurrent replacement, migration, restart, UTF-8 bounds, captured contexts and feed pagination.
`scripts/journal-smoke.py --restart-core` is an opt-in real Codex three-task scenario. It consumes
subscription usage. Do not use an uncertain or UNKNOWN task as a reason to blindly rerun the script.

## Delegated autonomy and owner rejection

`factory_decision_decide` atomically creates an approved registry entry without an owner notice.
Required fields: category, topic, value, rationale, request_id; optional registered artifacts and
supersedes_id/supersedes_version. Core injects identity/task/run. Authority requires an explicit
access allow for `factory.decision.decide.CATEGORY` in both captured and current configuration;
ordinary default allow, an escalation instruction, a prompt or a generic owner grant is insufficient.
Tool allow/deny and approval requirements still apply; categories with approval requirements are
not delegated for autonomous execution. Category names follow agent-name syntax.

The engineering policy delegates `technical` to role manager. Other roles (designer/QA included)
and categories can receive explicit policy rules without changing Go code. The normal manager can
propose product choices to the owner. `manager-autonomous` has the same technical delegation but
`communication.can_message: [demo]`, excluding owner. Factory rejects its owner questions,
proposals, approval requests and owner-action report notifications. A prompt saying "do not
escalate" expresses behavior; the communication configuration enforces the restriction. When no
permitted path remains, the agent must block affected work with a reason and report the problem.

Policy decisions have authority=policy, approved_by/recorded_by=actual agent, category,
authority_rules/current_authority_rules and hashes of captured/current agent configuration. Both
configuration checks happen again inside the storage transaction. Owner-reviewed entries have
authority=owner (legacy entries can omit it). Approved run context includes category and authority.
An approved registry choice confers no purchase, publishing or other protected tool permission.

Replacing a policy decision requires the current approved ID/version and the same category. An
active owner-approved choice cannot be autonomously replaced. A proposed, under-discussion or
rejected entry also reserves its topic for owner review, preventing relabelling its category/value
to bypass that review. After a rejection, alternatives for the same topic use decision_propose;
even a later owner approval does not remove the historical veto on autonomous takeover of that
topic. Topic matching is normalized text, not a semantic similarity detector.

Recording owner rejection atomically creates one durable review_result message to the non-owner
proposal author, with decision/task links and the rejection reason. The record exposes
reaction_message_id. Replaying the same request returns the original result without duplicating
messages. Owner-created proposals have no inferred agent recipient. If the author is a developer,
the developer receives the follow-up; it can coordinate with the manager through its allowed
communication. Missing recipient configurations use the existing message escalation mechanism.

The normal message scheduler waits for the project's writer slot and starts a separate
communication run with explicit revision instructions. The author reads the decision and task
state, revises the plan/affected tasks, proposes an alternative or blocks unfinished work and
reports the result. The previous approved choice survives rejection of a replacement. There is
no automatic rollback of completed effects. UNKNOWN keeps the writer quarantined; rejection
never bypasses reconciliation. Delivery is durable, but quality of the resulting plan remains
agent behavior, not a deterministic replanning algorithm.

Native Codex is a trusted host process with shell access: these permissions govern Factory gateway
calls, not OS/network isolation. Real verification: [decision-autonomy-verification.md](decision-autonomy-verification.md).
