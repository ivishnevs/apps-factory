# Factory MCP

`make build` now builds `bin/factory-mcp` alongside Core CLI and native Worker. No manual
`codex mcp add` or ~/.codex/config.toml edits are necessary. Worker configures the required stdio
server per app-server process using official [Codex MCP configuration](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).
Tool content is capped at 64 KiB with an explicit truncation marker to keep runtime events below
broker message limits. Narrow task queries when needed; full domain data remains in the owner API.
The adapter follows [MCP stdio transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
and exposes tool capability only. It forwards calls to Core; it does not orchestrate agents.

## Tools

| MCP name | Policy operation | Behavior |
| --- | --- | --- |
| factory_project_get | factory.project.get | Current project |
| factory_agent_list | factory.agent.list | Agent name/role/capabilities |
| factory_task_get / factory_task_list | factory.task.get / factory.task.list | Current project's tasks |
| factory_task_create | factory.task.create | BACKLOG; requires can_create_tasks |
| factory_task_update | factory.task.update | Metadata/dependencies with version |
| factory_task_assign | factory.task.assign | Assignee with version; requires can_assign_tasks |
| factory_task_ready / block / cancel | factory.task.ready / block / cancel | Existing state machine |
| factory_task_submit | factory.task.submit | Current task/run structured submission draft |
| factory_task_accept / reject | factory.task.review | Another agent's IN_REVIEW work; rejection needs feedback |
| factory_artifact_register / list / get / request_approval | factory.artifact.register / list / get / request_approval | Immutable artifact versions and owner content approval |
| factory_owner_request_approval / factory_approval_get | factory.owner.request_approval / factory.approval.get | Exact action approval and scoped status |
| factory_skills_list / search / read | factory.skills.list / search / read | Metadata or lazy confined resource |

Tool names use underscores for runtime interoperability; policy names keep dots. Task creation
with an assignee additionally requires assignment permission, even self-assignment. Dependencies
and parent must belong to this project. Tasks carry no caller-supplied actor/project/run identity.
Policy deny filters discovery and execution; approval-required calls return 423 (until exact owner approval). Checks use both the captured run config and current config. Skills offered are their
intersection. Config changes do not grant extra authority to an existing run.

All mutations require a nonempty stable `request_id`; edits/actions/submit/review also require
`version`. Read the latest task before changing it. If the response is lost, retry the exact same
arguments/key; a reused key with changed input returns 409. Neither MCP adapter nor Worker retries
a mutation automatically. A 409 is a state/version conflict, not permission to repeat side effects.

## Identity and execution

Worker uses the owner credential only to claim/heartbeat/fetch and request a run credential.
The credential response also carries the provider-neutral Factory MCP instructions appended to the run.
The credential is an HMAC tied to the run and lease; the Core checks RUNNING and lease expiration
on every call/discovery. Terminal/UNKNOWN/CANCELLING runs cannot invoke MCP. A Core restart preserves
validity with the same secret and DB. Token rotation revokes issued credentials. Tokens cannot
invoke owner routes, list all projects, claim runs or reconcile unknown work.

Codex gets FACTORY_RUN_TOKEN and FACTORY_URL; FACTORY_TOKEN, NATS_URL and NATS_PASSWORD are removed
from its inherited environment. Tokens are not prompt text, config values or command-line arguments.
Host permissions remain danger-full-access: agents can read host files and local .env. MCP policies
are enforced for Core tool calls and do not create OS isolation or filter unrelated user MCP servers.

`factory_task_submit` while running saves a draft (summary, declared checks, commit, artifact refs).
The task stays IN_PROGRESS and retains its writer reservation. Known runtime completion moves it
to IN_REVIEW and preserves this structured submission. Without a draft, final text is the fallback.
A failed/unknown/cancelled run cannot make its draft reviewable. Only another agent or owner can
accept/reject IN_REVIEW work. Checks and artifact URIs are declarations, not independently verified
proof or artifact approvals.

One writer per project means a planner must finish its turn before an executor can run. A later
review task can be READY with lower priority than the executor, as the example manager describes.
Normal dependencies still require DONE, so a review task cannot depend on the unaccepted task it
must review. No coordinator waits inside a turn or bypasses the scheduler.

## Audit and verification

GET /runs/{id}/tool-calls (owner token) returns durable tool name, agent/project/run, request key,
HTTP outcome and time. Audit inserts before dispatch with status 0 and finalizes afterward. Status 0
after a crash means outcome unknown; inspect state or replay the mutation's same key. Arguments,
tokens and arbitrary result text are omitted. Domain events retain state snapshots independently.

Run with Core/NATS and native Worker active, .env loaded in the caller terminal:

```sh
python3 scripts/mcp-smoke.py
# Also verify MCP credentials with a resumed thread:
python3 scripts/smoke.py --mcp
```

The manager creates and readies an executor and later reviewer via MCP. Executor reads a real
skill resource and submits structured checks via MCP. Reviewer accepts its work via MCP; owner
script accepts the planner/reviewer tasks. IDs and observed tool calls go to .cache/mcp-verification.json.
See [verification](factory-mcp-verification.md). Messaging and owner questions are now implemented;
see [messaging](messaging.md) for their tools and inbox. Action approval grants are implemented: see [approvals](approvals.md). Versioned [artifact approvals](artifact-approvals.md) are also available. Decision Registry and reports remain future work.

Reports and Decision Registry tools use the same run-scoped gateway. See [contracts and limits](reports-decisions.md). Decision recording always needs exact owner authorization; proposal/report creation never implies approval.

OpenSpec tools: factory_openspec_init/list/read/check provide project-scoped filesystem integration. Read pages are digest-pinned and bounded; initialization cannot overwrite documents or bypass writer quarantine. See [OpenSpec contract](openspec.md).
