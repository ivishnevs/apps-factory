# Action approvals

Core stores an approval of one exact Factory MCP call: the tool name, all arguments (including the entity
version and `request_id`), the project, the agent and the current task. A task's approval applies when that task's
run continues; for a run without a task the scope is the agent/project and the exact call. Another agent or task
cannot use it.

`factory_owner_request_approval` takes:

```json
{
  "call": {
    "name": "factory_task_update",
    "arguments": {"id": "TASK_ID", "version": 1, "title": "New title", "request_id": "change-title-1"}
  },
  "reason": "Why this exact change needs approval",
  "blocking": true,
  "timeout_seconds": 86400,
  "request_id": "ask-change-title-1"
}
```

The existing mutating tools with a stable `request_id` are supported. Unknown tools and requests to approve other
approvals are refused. The agent needs the right to write to the owner and the rights for the target operation; a
deny or a missing capability is not overridden by an approval. The request/get tools themselves need no approval,
even in `always` mode (otherwise the agent could not ask for permission); a deny still applies to them.

In a task the request is blocking by default, without a task nonblocking. After a blocking request the agent ends
its turn. Core frees the writer once the runtime is known to have ended, moves the task to BLOCKED and, after the
decision, continues it in the same thread. A decision that arrives before the turn ends is also taken into account.
A lost run whose process may still be running holds the task until the node confirms. Rejection and expiry also
hand control back to the agent: the action is forbidden.

A blocking request changes the version of the current task. So approving a change to the waiting task itself
needs a separate execution task or `blocking: false`; Core refuses a blocking approval of such a call up front.
The version or other arguments of an approved call cannot be changed: that needs a new approval.

## Owner

`factory inbox list [PROJECT]` shows the question with its `approval_id` and the exact call. The plain
`/inbox/ID/answer` is refused for it: a chat answer does not grant permission.

```sh
./bin/factory approvals list PROJECT
./bin/factory approvals get APPROVAL_ID
./bin/factory approvals decide APPROVAL_ID decision.json
```

`decision.json`:

```json
{"version":1,"action":"approve","reason":"Reviewed the exact action","request_id":"owner-decision-1"}
```

Actions: `approve`, `reject`, `revoke`. Revocation applies to an APPROVED approval at its current version.
HTTP: `GET /approvals?project=...`, `GET /approvals/ID`, `POST /approvals/ID/decision`. These endpoints require the
owner token; a run token cannot decide requests. The decision, the inbox answer and the task wake-up are atomic.

## Command guards

An agent asks for a one-time permission to run a shell command through `factory_owner_request_approval` with
`call: {"name":"command","arguments":{"argv":["git","push","origin","main"]}}` (exactly the key `argv`, non-empty
strings). Core refuses the request when the agent's rules deny the command (403) or it does not require approval
(400). The approval is stored with `kind: command` and fingerprint `command:<id>`, so the same argv can be requested
again after use; `CallApproval` (action) does not find it. The owner decides as for an action (`factory approve|reject`).

`factory-shim` checks the command through `POST /agent/command` with the run token. Core takes the oldest APPROVED,
unexpired grant of this run (project, agent, task) with exactly the same argv, marks it `USED` (event
`approval.used`) and answers 200; otherwise 423 with instructions. An approved grant expires after
`timeout_seconds` (default 86400 s), like an action. Every refusal (403/423) is a run event
`factory/command_refused` and a feed row `run.command_refused`.

## Checks and audit

`factory_approval_get` returns the decision within the current agent/project/task scope. Core checks the snapshot
and the current configuration on every call. A required approval gives HTTP 423 until the exact call is APPROVED;
a deny gives 403. A requested exact call is also blocked until the decision, even if the original policy allowed
it without approval. A permission does not extend to changed arguments or a new `request_id`.

Identical repeats are allowed until revocation/expiry; the original `request_id` keeps the mutation idempotent.
This permits one logical action; it is not a one-time token. Revocation prevents further calls; an action already
performed is not rolled back. Check and execution within one Core are serialized with the owner's decision.

Core's database keeps requests, decisions, their inbox link and the `approval_id` in the tool audit. Outbox events:
`factory.<company>.events.approval.requested`, `.resolved`, `.expired`. An owner decision (approve or reject) on a
task's request (`task_id`) adds the authority trigger `approval_decided` with the decision and reason to that task:
it lifts the task's wait/ask parks, and the next brief shows the decision (likewise for artifact approvals;
revocation and expiry add no trigger). The default timeout is 24 hours, the maximum 7 days; the time check applies
even before the background status update. Expiry never means permission. Stable request and decision keys make it
safe to repeat HTTP requests after a lost response.

## Limits

These are action approvals. Grants are issued for mutations with a stable `request_id`; read-only tools with an
approval requirement stay blocked (including with `mode: always`). For normal work use `selective` with an explicit
list of operations. [Versioned artifact approvals and task dependencies](artifact-approvals.md) are separate; task
acceptance remains a separate operation. Executors for publishing, purchases, production credentials and project
deletion are not implemented: do not grant permission for an executor that does not exist. Codex host tools run in
the trusted full-access model; MCP grants are not an OS sandbox. Reports and the Decision Registry:
[reports and decisions](reports-decisions.md).

Results: [verification](approvals-verification.md).

Checks: `make check`; end-to-end with real Codex: `python3 scripts/approvals-smoke.py --restart-core` (Core/NATS and
a node must be running).
