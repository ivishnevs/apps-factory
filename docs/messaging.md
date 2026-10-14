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
do not poll or wait for the recipient while holding the writer. The task stays IN_PROGRESS until
known completion, then BLOCKED with wait_message_id. If an answer already arrived, completion
projects directly to READY. Owner/recipient response makes a message-blocked task READY only
when the previous run is known COMPLETED. Core injects the explicit answer into the continuation
prompt, resumes the same thread and clears the wait marker when dispatching the next attempt.
Continuation attempts are explicit answers, not failure retries; max_retries does not limit them.

Submit is forbidden while waiting; an unanswered task cannot be made READY. UNKNOWN stays
quarantined; an answer does not reconcile it. FAILED/CANCELLED, owner-cancelled and manually
blocked tasks do not resume automatically. Replying while a recipient run still holds the project
writer does not start the originator concurrently. After that run finishes, normal dispatch applies.

A manager may ask the owner for advice in the same conversation. When its prior delivery completed
without answering, owner advice can reopen that original question and schedule manager continuation.
The manager then explicitly answers the originator. Timeouts/UNKNOWN are not reopened this way;
the owner can answer the original escalated question and reconcile unknown work separately.

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
