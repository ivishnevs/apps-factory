# Messages, owner questions and inbox

Core persists asynchronous communication in its database. `factory_message_send`, `factory_message_ask`,
`factory_message_reply`, `factory_message_get`, `factory_message_list` and `factory_owner_ask`
are available through Factory MCP. Identity and project come from the active run credential.
Communication never depends on scraping an agent's final text.

## Agent communication

The reporting tree (`spec.reports_to`, [agent configuration](agent-configuration.md#hierarchy)) routes
questions: `factory_exit_ask` goes to the asker's manager, which answers delegated categories or forwards up;
locked categories go to the owner. Every agent may message its chain and its direct reports.
Set `spec.communication.can_message` to explicit agent names and/or `owner`. Both captured and
current permissions must permit the target; factory.message.send/ask/reply/get/list and
factory.owner.ask also pass the policy evaluator. No wildcard target or role-specific routing
is assumed. The example developer can message manager/owner; manager can message demo/owner.
When adding designer/qa, configure the relevant sender's target list. Agent name owner is reserved.

Send supports notification, review_request, review_result and escalation. Use ask for question and
reply for answer. Body is 1..16 KiB; mutations require stable request_id, replies require the latest
message version. Forms support up to 16 questions; choice options must be distinct (2..20).
Every structured question requires exactly one answer of its declared type. A textual body is
required alongside form answers. Core rejects arbitrary sender/project/run identifiers in MCP.
Read tools are scoped to the run's project; messages are shared project context, not private ACLs.

## Conversation tasks

Every agent has one long-lived **conversation task** per project (`kind: conversation`, id
`conv-<agent>-<project>`, `counterpart` its reports_to), created when Core loads the catalog and on first use.
A message, question, escalation or non-blocking answer addressed to an agent is a `message` trigger on its
conversation task (`delivery_status: TRIGGERED`); separate message delivery runs are retired. Input from the
owner or the recipient's manager is authority. The conversation task is scheduled like any task: the agent's
run limit applies, but it does not take the project writer slot. On the node it runs in its own conversation
directory, `<state_dir>/conversations/<company>/<project>/<agent>` (no writer lock, no task worktree or branch in
either workspace mode), where its project data and `.factory/manifest.json` are materialized and its session
resumes; the project workspace reaches it read-only by path (`FACTORY_PROJECT_WORKSPACE` and a note in its
instructions), so it never contends with the project's writer run. Its brief lists each message with how to handle it; its runs resume one runtime session. The agent
answers with factory_message_reply (message_id/version/body/request_id, answers optional), forwards with
factory_question_forward, and ends with `factory_exit_done`, which marks the input handled up to its ack_seq
and keeps the task open (a turn without an exit does the same; ask and wait are also accepted; any new input on a
conversation task, authority or not, ends its active wait). An
unanswered question does not escalate when a run ends: it moves up after `escalate_after` (questions routed up
the chain) or escalates at its deadline. Tasks an agent delegated that close wake its conversation with
`delegated_closed` when none of its open tasks waits on them (a dependency or the parent wake by themselves).
Conversation tasks never go idle-alerted, cannot be paused, reassigned or cancelled.

## Decision answers

Options are suggestions: a free-text body without `answers` is always accepted (an escalation question still
needs one of its answers). The first answer wins: answering is a compare-and-set on the question's version; a
later answer gets 409 `{"reason":"already_answered","answer":{…}}` with the recorded answer. A question stores
the asking task's wake_seq and assignee; when the task has new triggers or another assignee, reads (message
get, inbox, the conversation brief) show `may_be_outdated: true`. Closing a task (DONE or CANCELLED) cancels its
open questions (`status: CANCELLED`, event `message.cancelled`); approval requests keep their own lifecycle.

## Blocking questions

`blocking: true` requires the current agent's current task/run. After asking, finish the turn;
do not poll or wait for the recipient while holding the writer. The question is an `ask` park on
the task (trigger-log-scheduling D2). The task stays IN_PROGRESS until known completion, then READY held
by the park: reads show BLOCKED with wait_message_id and reason `parked:ask`. If an answer already
arrived, nothing holds it. The response clears the park and adds an `answer` trigger; the next run's
brief, built at claim, carries the explicit answer and resumes the same thread.
Continuation attempts are explicit answers, not failure retries; max_retries does not limit them.

Submit is forbidden while waiting; an unanswered task cannot be made READY. A lost run whose runtime
may still run keeps its task; an answer does not release it. Cancelled and manually blocked tasks do not
resume automatically. An answer from the task's authority set (the owner, the asker's managers, its
delegator) is authority input: it counts as progress, so a task whose run failed runs again without its backoff;
an action approval decision only grants the permission (the backoff stays). An answer wakes the asking
task; normal dispatch (the agent's run limit, the project writer) applies.

## Directives and authority input

The owner (`factory direct TASK TEXT [--urgent]`, `POST /tasks/{id}/directives`) and agents with authority
over a task (`factory_task_direct`) give directives: authority triggers that every tool result of the task's
running run carries as `authority_input` until the agent has seen them, and that block its exits until then
(409, [Factory MCP](factory-mcp.md#explicit-exits)). Each input has its author's rank (owner 0, then depth in
the reporting tree); among conflicting directives the one nearest the owner is marked `winning`. A parked task
is woken at once: authority input overrides its wait, ask, block and children parks and its retry and no_exit
escalations (task-lifecycle.md, Trigger log and parks). `--urgent` revokes the running run (a stop on the node's
control subject, the heartbeat as guarantee; outcome `revoked`, not an attempt) and the next run's brief starts
with the directive; `factory approve APPROVAL REASON --urgent` does the same for the tasks an approved artifact
version wakes. Comments by agents outside the authority set add no trigger; the next brief lists them.

A manager that cannot decide a question asks its own manager (factory_exit_ask from its conversation, or
factory_question_forward); the owner's answer to that reaches it as a message on its conversation, and it
then answers the originator.

## Owner API and CLI

All routes below require the owner/service FACTORY_TOKEN, never a run token. Owner sender identity
is fixed server-side. All mutations require request_id; answers/reads also require version.

| Route | Operation |
| --- | --- |
| POST /conversations | Create {project_id, agent, title, optional id, request_id} |
| GET /conversations?project=ID | List project conversations |
| GET /conversations/{id} | Logical conversation, with `task_id`, the agent's conversation task that handles it |
| POST /conversations/{id}/messages | Send {body, optional message_type/questions/timeout_seconds, request_id}; defaults to question |
| POST /messages | Owner message without a conversation {project_id, body, optional message_type, request_id}: to `owner.default_report`, or to the direct report named by a leading `@name` |
| GET /conversations/{id}/messages | Ordered persisted history |
| GET /messages/{id} | Message state and delivery run |
| GET /inbox?project=ID | Pending owner questions, escalations and unread notifications |
| POST /inbox/{id}/answer | {version, body, optional answers, request_id}; owner may also answer any pending project question |
| POST /inbox/{id}/read | {version, request_id}; acknowledge a non-question owner notification |

```sh
factory conversations create conversation.json
factory conversations list PROJECT
factory conversations send CONVERSATION_ID message.json
factory conversations messages CONVERSATION_ID
factory conversations say PROJECT "TEXT"        # default direct report; "@name TEXT" for another
factory messages get MESSAGE_ID
factory inbox list PROJECT
factory inbox answer MESSAGE_ID answer.json
factory inbox read NOTIFICATION_ID read.json
```

JSON files may be `-` for stdin. CLI supplies a random request_id when omitted; preserve your own
key to replay after uncertain transport. Conversation creation example:

```json
{"project_id":"my-project","agent":"manager","title":"Product planning","request_id":"planning-conversation"}
```

Send example: `{"body":"Plan the onboarding flow","request_id":"onboarding-question"}`.
Owner conversations are history; their messages are handled by the agent's conversation task, whose runs
resume one session. No separate review gate is needed for a delivered message; task implementation still
requires review.

## Structured owner question

An agent can call factory_owner_ask with:

```json
{"body":"Choose the audience","blocking":true,"request_id":"audience-question","questions":[{"id":"audience","type":"single_choice","question":"Who is this for?","options":["Users","Professionals"]}]}
```

Owner answer body:

```json
{"version":1,"body":"Build for professionals","answers":{"audience":"Professionals"},"request_id":"audience-answer"}
```

Use the actual latest version from inbox. `text` answers are nonempty strings, `confirmation` is a
JSON boolean, `single_choice` is one exact option, `multiple_choice` is a nonempty array of unique
listed options. Arbitrary answer keys, invalid options and missing questions are rejected.

## Reliability and limits

Question deadlines default to 24h; timeout_seconds accepts 1..604800 (0 uses default). Expired or
unresolved questions escalate into owner inbox; timeout is never an answer. At most eight automatic
questions per conversation/task can be dispatched; further questions escalate directly to owner.
Normalized repeated question text/forms in a conversation or task are rejected (including repeated
owner text with a new key). Reusing the original key replays the original response. Each message
chain has conversation_id and message events use it as correlation_id. Full snapshots remain in
the database; outbox notifications contain pointers. Validating permissions/config snapshots never mutates
current permissions. Tool responses retain the existing explicit 64 KiB content limit.

The inbox is CLI/HTTP; no UI or external delivery service is included. Questions and notifications
are communication, not approval grants or decision records. Reports and Decision Registry remain
future stages. See [verification](messaging-verification.md).

## Telegram

Core runs a Telegram notifier when `FACTORY_TELEGRAM_TOKEN` is set (one bot per company; the token stays in the
environment and is never logged or put into errors). `factory telegram bind` (`POST /telegram/bind`) returns a
one-time code valid for 15 minutes; sending `/start CODE` to the bot binds that chat. Only the bound chat is heard.

Outbound, and nothing else:

- attention feed rows (`attention = true`, see [reports and decisions](reports-decisions.md#event-feed-and-costs)):
  a question or escalation that reached the owner (with its single-choice options as buttons), a node in trouble
  (`node.restart_loop`, `node.check_failed`, `node.low_disk`, …), a quota pause, a report needing owner action;
- reminders for a question still waiting for the owner at 4 h and at 24 h (one reminder covers ages already
  passed);
- one daily digest at `FACTORY_TELEGRAM_DIGEST_AT` (default 18:00) in the company time zone: done, in progress,
  waiting for the owner, failures and retries, nodes, cost of the day.

Routine events (handoffs, submits, retries) stay in `factory watch` and `factory log`. A failed send keeps the
feed cursor and is retried with a backoff (1 s doubling to 1 min).

Inbound:

- a button, or a reply to a question message, answers that question (`request_id` `telegram-<update_id>`); a
  structured form still needs `factory inbox answer`;
- `/status` returns the status snapshot in short form;
- `/stop` asks for confirmation (a button or `/stop confirm` within 5 minutes) and then runs the kill switch;
  `/stop` alone does nothing;
- any other text is owner input exactly as `factory conversations say` (`POST /messages`): `@name text` addresses
  a direct report, other text goes to `owner.default_report` (see
  [agent configuration](agent-configuration.md#hierarchy)). It is an owner question on the owner conversation
  `owner-<agent>-<project>`, delivered as an authority `message` trigger on the agent's conversation task
  (`conv-<agent>-<project>`). The project is `FACTORY_TELEGRAM_PROJECT`, or the company's only project when unset.

Every update is applied once: Core records applied `update_id`s (`notify_updates`, migration 0031) and the answer
carries the update as its idempotency key. The notifier's state (bound chat, feed cursor, reminders, digest day)
is `notify_state`.
