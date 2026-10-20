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
| factory_settings_get | factory.settings.get | Company or project rules (`scope: company|project`), read-only, the run's own company and project |
| factory_question_forward | factory.question.forward | Forward a question you received and cannot decide to your manager (`message_id`, `version`, `note`) |
| factory_task_get / factory_task_list | factory.task.get / factory.task.list | Current project's tasks |
| factory_task_create | factory.task.create | BACKLOG; requires can_create_tasks |
| factory_task_update | factory.task.update | Metadata/dependencies with version |
| factory_task_assign | factory.task.assign | Assignee with version; requires can_assign_tasks |
| factory_task_ready / block / cancel | factory.task.ready / block / cancel | Existing state machine; cancel needs authority over the task (or its creator), `cascade` for open children |
| factory_task_pause / resume / reassign / reprioritize | factory.task.pause / resume / reassign / reprioritize | Authority actions over the assignee's subtree (agent-configuration.md#hierarchy); pause/resume with `agent` for an agent of the subtree |
| factory_task_submit | factory.task.submit | Current task/run structured submission draft |
| factory_task_accept / reject | factory.task.review | Another agent's IN_REVIEW work; rejection needs feedback |
| factory_artifact_register / list / get / request_approval | factory.artifact.register / list / get / request_approval | Immutable artifact versions and owner content approval |
| factory_owner_request_approval / factory_approval_get | factory.owner.request_approval / factory.approval.get | Exact action approval and scoped status |
| factory_skills_list / search / read | factory.skills.list / search / read | Metadata or lazy confined resource |

With explicit exits on (`FACTORY_EXPLICIT_EXITS=1`, see below) `factory_task_submit`, `factory_task_accept` and
`factory_task_reject` leave the list; the exit tools and, for agents with authority, the directive tool join it:

| MCP name | Behavior |
| --- | --- |
| factory_exit_submit | `summary`, `checks`, `artifacts`, `reviewer?`: IN_REVIEW to the reviewer (default: the owner's review), implementer recorded |
| factory_exit_handoff | `to`, `note`, `verdict?` (`approved` / `changes_requested` with `feedback`), `merge_failed?`: the task to `to` (or back to its implementer with the verdict) with an `assignment` trigger |
| factory_exit_done | `summary`: DONE (a repository task only with its pull request merged); on a conversation task "input handled", it stays open |
| factory_exit_ask | `question`, `options?`, `category?`, `evidence?`: `ask` park and a question up the chain |
| factory_exit_block | `on: [task]` or `on_children: true`, `reason`: waits on open tasks of the project or on open children; anything else (a closed or foreign task, a cycle, no open child) becomes an ask |
| factory_exit_wait | `reason`, `deadline`, `wake_at` or `after`: `wait` park until the time (task-lifecycle.md, Waits) |
| factory_task_direct | `task_id`, `text`, `urgent?`, `request_id`: a directive (authority input) to a task you have authority over |

Tool names use underscores for runtime interoperability; policy names keep dots. Task creation
with an assignee additionally requires assignment permission, even self-assignment. Dependencies
and parent must belong to this project. Tasks carry no caller-supplied actor/project/run identity.
Policy deny filters discovery and execution; approval-required calls return 423 (until exact owner approval). Checks use both the captured run config and current config. Skills offered are their
intersection. Config changes do not grant extra authority to an existing run.

All mutations require a nonempty stable `request_id`; edits/actions/submit/review also require
`version`. Read the latest task before changing it. If the response is lost, retry the exact same
arguments/key; a reused key with changed input returns 409. Neither MCP adapter nor Worker retries
a mutation automatically. A 409 is a state/version conflict, not permission to repeat side effects.

## Explicit exits

A run ends its turn with exactly one accepted `factory_exit_*` call ([contract](explicit-exits-contract.md)).
The adapter (`cmd/factory-mcp`) sends exits to `/agent/exit` with the node's git facts and its `ack_seq`, the
highest trigger seq it has returned to the runtime (the brief end at start, `authority_input` and 409 bodies,
counted only once the response reached the runtime). Core answers:

- **410** `not_current_run`: the run was revoked, ended or replaced; nothing changes, the agent stops (a queued
  exit stops retrying).
- **409** `unseen_input` with the unseen triggers when an authority trigger is above `ack_seq` (for `done`, any
  trigger); the agent reads them and exits again. Other 409 reasons: `invalid_exit`, `dirty_tree`,
  `secret_found`, `facts_required`, `pr_not_merged`, `needs_approval`, `exit_already_accepted`.
- **200**: the exit applies in one transaction (`handled_seq = ack_seq`, run outcome `succeeded`); a retry by
  the same run returns the stored result. Core unreachable: the adapter answers "queued — stop working" and
  retries the same exit; refused later (409/410) the run ends `no_exit`, undelivered at process end `lost`.

While the task has authority input above `ack_seq`, every tool result of the run is wrapped as `{"result",
"authority_input": [{seq, author, rank, kind, text}], "winning"}`: rank is the author's depth (owner 0, then
the reporting tree), and among conflicting directives the one nearest the owner wins. An urgent directive (or
urgent artifact approval) revokes the run: `run.stop` goes out at once on the node's control subject
`factory.<company>.nodes.<node_id>.control` (plain NATS, outside the command stream), the heartbeat answer is the guarantee, and the next run starts with the directive.

## Identity and execution

Worker uses the owner credential only to claim/heartbeat/fetch and request a run credential.
The credential response also carries the provider-neutral Factory MCP instructions appended to the run.
The credential is an HMAC tied to the run and lease; the Core checks RUNNING and lease expiration
on every call/discovery. Ended and CANCELLING runs cannot invoke MCP. A Core restart preserves
validity with the same secret and DB. Token rotation revokes issued credentials. Tokens cannot
invoke owner routes, list all projects, claim runs or reconcile unknown work.

The runtime's environment is an allowlist of the node's (`worker.RuntimeEnv`: session, locale, provider,
git/forge, proxy and toolchain variables; node.yaml `runtime_env` adds names or `PREFIX_*`), plus
FACTORY_RUN_TOKEN and FACTORY_URL for the run. The owner and node tokens (FACTORY_TOKEN, FACTORY_NODE_TOKEN),
the owner CLI's configuration, other run tokens and NATS_* never pass, whatever the list says, and the
`owner-cli` policy refuses `factory` on the agent's PATH. The run token stays in the runtime environment:
the approval shims and the adapter read it there. Tokens are not prompt text, config values or command-line arguments.
Host permissions remain danger-full-access: agents can read host files and local .env. MCP policies
are enforced for Core tool calls and do not create OS isolation or filter unrelated user MCP servers.

`factory_task_submit` while running saves a draft (summary, declared checks, commit, artifact refs).
The task stays IN_PROGRESS and retains its writer reservation. Known runtime completion moves it
to IN_REVIEW and preserves this structured submission. Without a draft, final text is the fallback.
A failed/unknown/cancelled run cannot make its draft reviewable. Only another agent or owner can
accept/reject IN_REVIEW work. Checks and artifact URIs are declarations, not independently verified
proof or artifact approvals.

Without per-task worktrees one run writes in the project workspace at a time, so a planner must finish its turn
before an executor can run (with worktrees, tasks of a project run in parallel). A later
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
