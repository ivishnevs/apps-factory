# Messages, owner questions and inbox

Core persists asynchronous communication in SQLite. `factory_message_send`, `factory_message_ask`,
`factory_message_reply`, `factory_message_get`, `factory_message_list` and `factory_owner_ask`
are available through Factory MCP. Identity and project come from the active run credential.
Communication never depends on scraping an agent's final text.

## Agent communication

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

An agent recipient gets a separate communication run carrying message_id, never the sender's
TaskID/thread. Core dispatches message work before normal READY tasks; message runs use the project
workspace (one at a time) and an agent has one active run. Agent must explicitly call factory_message_reply with
message_id/version/body/request_id (and answers when applicable). A completed turn without an
answer escalates the question to the owner. Known failed/unknown deliveries are not automatically
retried. Nonblocking answers create a separate recipient delivery; blocking answers resume the task.

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
resume automatically; a failed run retries after its backoff (task-lifecycle.md). Replying while a recipient run still holds the project
writer does not start the originator concurrently. After that run finishes, normal dispatch applies.

A manager may ask the owner for advice in the same conversation. When its prior delivery completed
without answering, owner advice can reopen that original question and schedule manager continuation.
The manager then explicitly answers the originator. Timed-out or lost deliveries are not reopened this way;
the owner can answer the original escalated question directly.

## Owner API and CLI

All routes below require the owner/service FACTORY_TOKEN, never a run token. Owner sender identity
is fixed server-side. All mutations require request_id; answers/reads also require version.

| Route | Operation |
| --- | --- |
| POST /conversations | Create {project_id, agent, title, optional id, request_id} |
| GET /conversations?project=ID | List project conversations |
| GET /conversations/{id} | Logical conversation, including stored thread |
| POST /conversations/{id}/messages | Send {body, optional message_type/questions/timeout_seconds, request_id}; defaults to question |
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
An owner conversation resumes its agent's last known-completed thread on subsequent owner deliveries.
Task questions get independent communication runs; they never overwrite that conversation's thread.
No separate review gate is needed for a delivered message; task implementation still requires review.

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
SQLite; outbox notifications contain pointers. Validating permissions/config snapshots never mutates
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
- any other text is owner input to a direct report of the owner: `@name text` addresses one, other text goes to
  `FACTORY_TELEGRAM_DEFAULT_REPORT` (else the first direct report). It is sent as an owner message in the
  conversation `telegram-<agent>` of `FACTORY_TELEGRAM_PROJECT` (hierarchy's conversation tasks replace this).

Every update is applied once: Core records applied `update_id`s (`notify_updates`, migration 0031) and the answer
carries the update as its idempotency key. The notifier's state (bound chat, feed cursor, reminders, digest day)
is `notify_state`.
