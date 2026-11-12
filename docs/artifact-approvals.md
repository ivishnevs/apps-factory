# Artifact version approvals

An artifact is a reference `{id, version, uri}` within a project. The registry also records the task and run that
created the version, the author and the time. For an existing ID/version the URI is immutable: another reference
needs a new version. Registration and task submission do not approve an artifact; task acceptance does not grant
an approval either.

## Producer: designer, developer or another agent

1. Register a version with `factory_artifact_register`:

```json
{"artifact":{"id":"landing-mockup","version":"v2","uri":"git:COMMIT:design/landing.svg"},"request_id":"register-mockup-v2"}
```

A file from the worktree is better uploaded to Core with `factory_artifact_upload` (core-owned-project-data):

```json
{"artifact_id":"landing-mockup","artifact_version":"v2","path":"design/landing.svg","request_id":"upload-mockup-v2"}
```

The node's MCP adapter reads the file inside the worktree (paths outside it and symlink escapes are refused, at
most 64 MiB), sends it to Core (`PUT /agent/blobs`) and registers the version with URI
`factory://artifacts/landing-mockup/v2`, `sha256`, `size` and the file name. The owner's approval is bound to that
digest: different content under the same version is refused (409); it needs a new version, and the earlier approval
does not extend to it. A `factory://` URI cannot be registered without uploaded content; external URIs (`git:`,
`https:`) remain references without content in the Factory.

2. Attach it to a structured `factory_task_submit` and/or request approval:

```json
{"artifact_id":"landing-mockup","artifact_version":"v2","reason":"Review the revised layout","blocking":false,"request_id":"approve-mockup-v2"}
```

The second JSON is the arguments of `factory_artifact_request_approval`. It creates a persistent inbox question
with an `approval_id` and the exact version reference. Usually the producer finishes its task with
`blocking: false`: the next task needs to wait, not the designer. `blocking: true` uses the existing
question/continuation mechanism of the current task: after the request, end the turn; do not poll the inbox inside
it. The task that requested the approval (for example parked with `factory_exit_wait`) wakes on the owner's
decision, approval or rejection, through the authority trigger `approval_decided` (decision and reason in the
brief); tasks that require the version get `artifact_approved`.

### One approval model: options (artifact-choice-approvals)

Every artifact approval is a set of 1–6 options, each a registered version of the project. A request with
`artifact_id`/`artifact_version` is an approval with one option (its label may be empty); approving it is choosing
that option. Decisions, delegate rules and owner surfaces are the same whatever the number of options: choose (for
one option: approve), ask for changes, reject.

When people should choose among several versions (variations, alternatives), the producer asks one approval with
options instead of an approval plus a question: `options` replaces `artifact_id`/`artifact_version`, and
`recommended` names the producer's pick (the option's number from 1, its label or `id@version`):

```json
{"options":[{"artifact_id":"paywall","artifact_version":"v1","label":"V1 Mascot","note":"friendly, light"},
  {"artifact_id":"paywall","artifact_version":"v2","label":"V2 Editorial"},
  {"artifact_id":"paywall-dark","artifact_version":"v1","label":"V3 Dark"}],
 "recommended":"1","reason":"Three paywall variations","request_id":"choose-paywall"}
```

Core refuses (400, naming the option) an unknown version of the project, an archived one, a repeated version or
label, a missing label among several options, no options or more than 6, and both `options` and `artifact_id` at
once; a version already waiting for (or holding) an approval is a 409. The decider **chooses** one option (an
optional comment; on a one-option approval `approve` is the same as choosing option 1), **asks for changes**
(required text; it may name the options) or **rejects all** (required reason); one of several options is never
approved without choosing. Choosing approves the chosen version (`artifact` and the approval's version columns
become it, `chosen` its number; a one-option approval has its version there from the request); the other options
are **not chosen** (`NOT_CHOSEN` as their state, not rejected): a task requiring one keeps waiting, and it may be
asked for again later. Asking for changes ends the approval as `CHANGES_REQUESTED` with nothing approved; the
version may be asked for again. The requesting task's `approval_decided` input says it: "V2 (paywall@v2) chosen by
the owner: darker paywall; not chosen (still usable later): V1 (paywall@v1), V3 (paywall-dark@v1)", "changes
requested by the owner on V1 (paywall@v1): …", "all options rejected by lead (delegated: …): …"; for one option
"artifact approval … of paywall@v2 approved by the owner: …" (or "changes requested by …", "rejected by …").

Owner HTTP: `POST /approvals/ID/decision` with `{"action":"choose","option":2,"reason":"","version":N,"request_id":…}`
(`{"action":"approve",…}` on a one-option approval), `{"action":"changes","reason":"mix V1's icon with V3's
palette","options":[1,3],…}` or `{"action":"reject",…}`; a topic decides it with the same `option`/`options`
(`POST /topics/ID/decide`; "approve all" chooses each single-version approval's only option). A one-version
approval is still an artifact version line in people topics; an approval of several options joins its task's topic.
Tool-call approvals (`factory_owner_request_approval`, command guards) are a different kind: approved or rejected.

`factory_artifact_register` is bound to the current task/run; the caller does not pass another identity.
References in a structured submission are registered atomically as well. A submission with artifacts requires
rights for both `factory.task.submit` and `factory.artifact.register`; the implicit registration cannot bypass a
policy deny. An approval request requires the right to write to the owner and needs no approval of itself. A deny
for this tool applies.

`factory_artifact_list` lists the project registry; `factory_artifact_get` takes `artifact_id` and
`artifact_version` and returns the record and the latest decision. All agents of the project can read an
artifact's approval through `factory_approval_get` if policy allows the tool. Grants for specific actions stay
within the author's/task's scope.

## Dependent task

`tasks create/update` and `factory_task_create/update` accept:

```json
{
  "required_artifacts": [
    {"id":"landing-mockup","version":"v2","uri":"git:COMMIT:design/landing.svg"}
  ]
}
```

A reference may be declared before the version is registered. A task whose approval is missing, pending, rejected,
revoked or expired stays READY but does not start (reason `artifacts`); a task read shows BLOCKED with an
`artifact:` reason (the status is not stored). It creates no run and holds no worker/writer. Once all required
versions are approved, the task starts without a separate action; then the usual task dependencies, agent
availability and writer rules apply.

These are two independent conditions: `dependencies` require DONE, and `required_artifacts` require approval of a
specific version. Approving an artifact does not accept a task, and the producer's DONE does not approve the
artifact. Without `required_artifacts` there is no mandatory design/approval stage. The names designer/QA are not
hard-coded in Core.

The run stores `approved_artifacts`: the approved version (with `sha256` for an uploaded one), the `approval_id`
and the decision version. They are added to the prompt; before the runtime starts, the node puts uploaded versions
into the worktree as `.factory/inputs/<id>/<version>/<file name>` (claim manifest, see [runtime protocol v2](runtime-protocol-v2.md)).
A reference in `required_artifacts` may pin `sha256`: then only an approval of that content satisfies it. Core
checks them inside the dispatch transaction and again at claim, right before the runtime starts. Revocation
cancels a not-yet-started QUEUED run and returns its task to waiting. A new approval needs a new run with a new
snapshot of the inputs. A RUNNING run keeps the snapshot allowed when it started; revocation neither rolls back its
side effects nor interrupts it.

Manual blocks, escalation parks and CANCELLED/DONE are not lifted automatically. Moving a dependent task to a new
version is possible only under the usual edit rules, with the current task version and no active/unknown run.
Approving v1 does not allow v2; tasks explicitly pinned to v1 keep using v1.

## Owner decision and CLI

The shared inbox and the [approvals](approvals.md) interface are used:

```sh
./bin/factory inbox list PROJECT
./bin/factory approvals get APPROVAL_ID
./bin/factory approvals decide APPROVAL_ID decision.json
```

```json
{"version":1,"action":"approve","reason":"Reviewed this exact version","request_id":"owner-layout-v2"}
```

On a one-version approval `approve` chooses its only option (the reason is optional); `changes` with the text asks
for changes and `reject` with the reason rejects it, as on any artifact approval. `factory approve` / `reject` work
the same way; an approval of several options is decided with `choose`.

A reason other than the default (`approved by owner`), approving or rejecting, is the owner's comment on the work
(approval-comments-handled): Core records it as a board comment on the requesting task, the task's next exit is
refused until it has an outcome, and a `done` outcome says what was changed for it. The approval shows that outcome
under the decision: on the task's materials, in the file viewer and in the Inbox's "Your comments on approvals"
("done by launch-creative: frame 1 without the window", "not handled yet").

`reject` keeps consumers blocked; `revoke` forbids new starts on the approval. A plain inbox answer does not approve
the artifact. The decision and the queue wake-up/stop are stored atomically. A version can have only one active
request/approval. After rejection, revocation or expiry a new explicit request may be created; earlier decisions
stay in the history. Changed content must get a new version, not a re-approval of the old one.

The default decision timeout is 24 hours, the maximum 7 days. Expiry closes the question as EXPIRED, without
permission. An accepted artifact approval has `expires_at: 0` and lasts until revoked; the question's timeout does
not cancel content already approved.

Registry commands:

```sh
./bin/factory artifacts list PROJECT
./bin/factory artifacts get PROJECT ARTIFACT_ID VERSION
./bin/factory artifacts register PROJECT register.json
./bin/factory artifacts request-approval PROJECT request.json
```

For the owner, register JSON contains `artifact`, `task_id`, an optional `run_id` and `request_id`. For owner
request-approval: `artifact_id`, `artifact_version`, `reason`, `request_id`, and optional `agent` and
`timeout_seconds`. The request's author defaults to the registration's author or the assignee of the source task;
the owner can name a configured agent explicitly. The owner API does not create a blocking request on behalf of an
active run.

HTTP: `GET/POST /projects/PROJECT/artifacts`, `GET /projects/PROJECT/artifacts/ID/VERSION`,
`GET /projects/PROJECT/artifacts/ID/VERSION/content` (uploaded content), `POST /projects/PROJECT/artifacts/approval`;
all require the owner token. Events: `artifact.registered` and the existing `approval.*`,
`task.artifact_waiting`. History is stored in Core's database.

## Limits and checks

Core stores uploaded versions itself (blob store by SHA-256) and binds the approval to them; the owner reads the
content through `GET /projects/PROJECT/artifacts/ID/VERSION/content`. Core never reads node disks. For external
URIs Core stores and approves only the reference; it neither downloads nor checks the content: use commit- or
content-addressed URIs, not a mutable unversioned URL; the trusted agent remains responsible for a new version when
the content changes. Factory MCP is not file isolation.

Earlier task_artifacts and submissions are kept by the migration. A historical reference must be registered
explicitly before a new approval if it is not yet in the registry; the migration does not choose a URI for the
owner from potentially ambiguous old references. Registration also checks old references: if they disagree on the
URI for one version in the project, use a new version. A historical URI is never overwritten.

`make check` covers versions, policy, independence from acceptance, waiting, restart, rejection/revocation/expiry
and quarantine. `scripts/artifact-approvals-smoke.py --restart-core` checks with real Codex: v1 approve → QA,
v2 reject → waiting, v3 approve → QA. Core and a node must be running. Results:
[verification](artifact-approvals-verification.md).

## Retention (artifact-retention)

A project keeps superseded artifact versions for `artifact_retention_days` (project page → Settings → Keep old
artifact versions; `PATCH /projects/ID` with `artifact_retention_days`). 0, the default, keeps them forever. Core's
`FACTORY_ARTIFACT_RETENTION_MAX_DAYS` sets a ceiling (the hosted cloud sets 30): a project at 0 then uses the ceiling,
a value above it is refused (400), and `GET /settings/artifact_retention` answers `{"max_days": N}` (0: none).

Once a day Core archives the versions older than that which are none of: the latest version of their artifact, its
latest approved version, a version with an open approval request, a required input of a task that is not done or
cancelled. An archived version keeps its row: id, version, digest, size, author, time, task links, approvals and
the image listing of an archive. It gets `archived_at` and `archived_reason` (`retention: N days`). Its content is
deleted from the blob store once no live version, run log, report attachment, message attachment (or its preview) or
skill uses the same digest. Each run
that archives or deletes something records one feed event `artifact.retention` with the counts and the bytes freed.

Reads of an archived version's content (`/content`, `/entry`, a run's blob download) answer `410 Gone` with
`{"error", "archived_at", "reason", "artifact_id", "version", "latest_version"}`. Listings, task materials and
submissions still show the version, marked archived; the web shows "Archived DATE" instead of a preview or a
download. `factory_artifact_get` adds an `archived` note naming the latest version. An archived version cannot be
required by a new task or submitted for approval (400, naming the latest version). Uploading the same content under
the same id and version again restores it.

## People topics

An approval that reaches people (directly, or escalated by its delegate) carries a brief and joins a topic by its
version line; a composite declares `includes`. See [people-topics.md](people-topics.md).

## Who approves (approval-delegation)

A team (`spec.approvals` of its team document) or a project (`approvals`, edited on the project page, overriding its
team) sets who approves each kind: `internal_artifacts` (the team's own material), `external_artifacts` (what leaves
the company) and `decisions`. Modes: `owner` (people only, a hard gate no agent passes), `agent` with `agent: NAME`
(that agent decides and escalates when unsure) and `lead` (the team's lead decides what the owner's earlier answers
and recorded decisions cover, citing them). Defaults: internal material and decisions `lead`, external material
`owner`. Decisions about money and destructive changes always go to people.

`factory_artifact_request_approval` takes `audience` (`internal` or `external`, the default). When the mode names an
agent other than the requester, Core sends that agent the request; it stays out of the people's Inbox until the agent
escalates. The agent decides with `factory_approval_decide`; the approval records `decided_by`, the setting and the
citations.

The rules are the same for every artifact approval, one option or several, in `agent` and `lead` mode alike
(artifact-choice-approvals): the delegate decides with `approve` (one option) or `choose` (`choice`: the option's
number, label or `id@version`; `choice` 1 on one option is the same as `approve`), `changes` (`options` optional),
`reject` or `escalate`; `approve` on several options is refused with a hint (no approving without choosing).
Approving or choosing needs `cites`, and Core checks every one names what people said: the id of a message people
wrote (or of a question people answered), the id of an approved decision, or a quote of the company's or project's
rules, which only people write (`"project rules: dark theme only"`, `"company rules: …"`; the quote must be in the
rules). Any other cite is refused: without such a basis the delegate escalates. `changes` and `reject` need a reason
and no cite (the delegate's own judgement on weak work; no person is asked). `escalate` on several options may
`drop` options it judges not good enough (`[{"option":"2","reason":"off-brief palette"}]`, at least one option
stays; dropping all is `reject`; a one-option approval is escalated whole): people see the kept options first and
the dropped ones collapsed with the delegate's reasons, and may still choose one. Under the people-only mode
(`owner`) Core refuses every agent decision; an
agent it reached by strict delegation can only escalate it. Otherwise the agent an approval is assigned to now decides
it, also after a strict delegate handed it up or after the setting moved to another agent (people-topics D18).
`factory_decision_decide` is allowed where the project's decision mode names the agent (the lead with `cites`), as
well as in policy-delegated categories; a people-only decision mode refuses agents even there.
