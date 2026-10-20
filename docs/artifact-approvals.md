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
into the worktree as `.factory/inputs/<id>/<version>/<file name>` (claim manifest, see [OpenSpec](openspec.md)).
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
all require the owner token. Events: `factory.<company>.events.artifact.registered` and the existing `approval.*`,
`task.artifact_waiting`. History and outbox are stored in Core's database.

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
v2 reject → waiting, v3 approve → QA. Core/NATS and a node must be running. Results:
[verification](artifact-approvals-verification.md).
