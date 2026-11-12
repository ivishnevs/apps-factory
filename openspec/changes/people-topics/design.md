# Design: people topics

## Where we are (checked 2026-11-12)

Items reach people (recipient `owner`: the company's owners and admins) by being **created** for them or by
**moving** to them:

| Path | Code | Bringer |
|---|---|---|
| `factory_owner_ask`, `factory_message_send` to owner | `core/messages.go` → `SendMessage` | agent |
| `factory_exit_ask` routed to people | `storage/exits.go` `exitAsk` | agent |
| `factory_owner_request_approval` (call, command) | `storage/approvals.go` `RequestApproval` | agent |
| `factory_artifact_request_approval` without a delegate | `storage/artifacts.go` `RequestArtifactApproval` | agent |
| `factory_decision_propose`, reports with an owner action | `storage/journal.go` `journalNotice` | agent |
| Core escalations (task) | `storage/escalations.go` `openEscalation` | Core |
| Quota pause | `storage/pauses.go` | Core |
| Task done, person ready/reminder | `storage/triggers.go`, `storage/people.go` | Core (notifications) |
| Delegate escalates an approval | `storage/artifacts.go` `EscalateApproval` (move) | agent (lead) |
| `factory_question_forward` to people | `storage/messages.go` `forwardTx` (move) | agent |
| Timeout auto-forward to people | `storage/messages.go` `AutoForward` → `forwardTx` (move) | Core, nobody chose |
| Task submitted to a person reviewer | `storage/exits.go` submit; Inbox "reviews" from task state | agent |

Each builds its own body string. People's chat is `owner-<agent>-<project>` (`ownerChat`); escalations live in
`chain-…` threads; the web Inbox is a flat list (`web/src/components/app/inbox/model.ts`).

## D1. One gate

`internal/storage/people.go` holds `toPeople(ctx, tx, in PeopleItem) (Topic, error)`, the only function that makes
an item visible to people. Every row of the table above calls it, in the transaction that creates or moves the item:

```go
type PeopleItem struct {
    Kind     string        // question | approval | escalation | decision | review | update
    Ref      string        // message, approval, escalation, decision or task id
    Bringer  string        // agent name, or "factory" for Core
    Opened   bool          // set by the gate: the item opened its topic
    Brief    *Brief        // the bringer's brief (agent) or the template's (Core)
    Note     *ItemNote     // the item's own note when it joins a topic
    Topic    string        // explicit topic id (optional)
    Lineage  string        // artifact id for version lines; "" otherwise
    TaskID, ProjectID string
}
```

The gate: finds or opens the topic (D4), checks the brief (D3), stores `topic_id` on the message, adds the item row,
fills Core's context (D6), and emits `topic.item_added`. Notifications (`update`) need no brief.

An architecture test fails when a function in `internal/storage` sets a message's recipient to `owner` (send or
move) without going through `toPeople`; the allowed call sites are listed in the test.

## D2. Brief

```go
type Brief struct {
    Title          string `json:"title"`                    // one line: what this is about
    Ask            string `json:"ask"`                      // what you decide, one sentence
    After          string `json:"after"`                    // what happens after (per answer or overall)
    Recommendation string `json:"recommendation,omitempty"` // the bringer's advice and why
    Changes        string `json:"changes,omitempty"`        // since the version people saw
    Scope          string `json:"scope,omitempty"`          // what you do not decide
    Context        string `json:"context,omitempty"`        // background, markdown
}
type ItemNote struct {
    What    string `json:"what"`              // what this item is
    Changes string `json:"changes,omitempty"` // since the version people saw
}
```

One JSON schema in `internal/factorytools` (`briefSchema`, `noteSchema`), added as optional `brief` and `note` to:
`owner_ask`, `message_ask` (when it routes to people), `exit_ask` arguments, `question_forward`,
`owner_request_approval`, `artifact_request_approval`, `approval_decide`, `decision_propose`, `task_submit`
(submission). Each description gets the same sentence: "brief is required when this reaches people (the owner); Core
refuses with brief_required and names the fields". `message_send` and `report_create` to the owner take `topic` only.

## D3. What is required, and the refusal

| Path | Required from the bringer | Core derives |
|---|---|---|
| Question (`owner_ask`, `exit_ask`, `message_ask` to people) | `title`, `after`; `recommendation` when options are given | `ask` = the question |
| Forward to people (`question_forward`) | `title`, `ask`, `after`, `recommendation` | — |
| Call approval (`owner_request_approval`) | `title`, `context` (why) | `ask` ("Allow …?") and `after` from the call (D7) |
| Artifact approval to people, opening a topic | `title`, `ask` (names exactly what is approved: the whole mockup, element X, state Y), `after`, `recommendation` | — |
| Artifact approval joining a topic | `note.what` | — |
| Escalate approvals (`approval_decide escalate`) | as opening, or `note.what` per item when joining | — |
| Decision proposal | `after` | `title`/`ask` from the topic, `recommendation` from value and rationale |
| Submit to a person reviewer | `ask` (what to check) | `title` from the task, `after` (accept closes, return reopens) |
| Notifications, reports | — | attached to the task's topic as updates |
| Core (escalations, quota) | — | template (D8) |

Always: `changes` (in the brief or the note) when the item's version line has a version people already saw.

A missing field is refused before anything changes: `BriefError{Missing []string, Why string}` aborts the transaction;
the tool answers 400 `{"reason":"brief_required","missing":[…],"hint":"This goes to people (owner) because …;
add brief.ask: what they decide, one sentence; …"}`. Resending with the brief creates no duplicate (the first attempt
wrote nothing). The gate knows the recipient, so agents need not predict it: the same call to a delegate passes
without a brief, and a brief given there is ignored.

Text limits: `title` ≤ 120, `ask` ≤ 300, other fields ≤ 4000 characters.

## D4. Topics

Migration 0041:

```sql
CREATE TABLE people_topics (
  company_id text NOT NULL, id text NOT NULL, project_id text NOT NULL,
  key text NOT NULL,            -- line:<artifact> | task:<id> | agent:<name>:<reason> | own:<id>
  status text NOT NULL,         -- open | settled
  brief jsonb NOT NULL,         -- the topic's brief; needs_brief when empty
  needs_brief boolean NOT NULL DEFAULT false,
  brought_by text NOT NULL, conversation_id text NOT NULL,
  created_at bigint NOT NULL, updated_at bigint NOT NULL, settled_at bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, id));
CREATE INDEX people_topics_open ON people_topics(company_id, project_id, key) WHERE status = 'open';
CREATE TABLE people_topic_items (
  company_id text NOT NULL, topic_id text NOT NULL, kind text NOT NULL, ref_id text NOT NULL,
  lineage text NOT NULL DEFAULT '', note jsonb, state text NOT NULL,  -- open | decided | withdrawn | update
  added_at bigint NOT NULL, PRIMARY KEY (company_id, topic_id, kind, ref_id));
CREATE INDEX people_topic_items_ref ON people_topic_items(company_id, kind, ref_id);
CREATE INDEX people_topic_items_line ON people_topic_items(company_id, lineage) WHERE lineage <> '';
ALTER TABLE messages ADD COLUMN topic_id text;  -- also in the message document
```

**Finding the topic**, first match wins:
1. explicit `topic` (an open topic of the same project the bringer's team has items in);
2. `line:<artifact id>`: a topic holding an earlier version of the artifact, **open or settled within 30 days**; a
   settled one reopens (rework after a rejection lands in the rejected row);
3. `task:<task id>`: an open topic of the task;
4. Core items: `agent:<assignee>:<reason>` (four `retries_exhausted` of one agent are one topic);
5. otherwise a new topic `own:<item>`.

**Opening** takes the item's brief as the topic's brief. **Joining** keeps the topic's brief and stores the item's
note; a bringer may pass a full brief when joining to restate the topic (the previous one is kept in the topic's
history). A topic is `open` while any item is `open`; otherwise `settled` and it leaves the Inbox (it stays in the
chat history and reopens by version line).

**Splitting**: `factory_people_brief` with `move: [items]` lets the bringer or its lead move items to a new topic
when the key grouped unrelated things.

## D5. Items, buttons, decide all

Buttons come only from the item's mechanics; the API returns them as `actions` per item:

| Item | Actions | Effect (existing code) |
|---|---|---|
| approval (artifact, call, command) | approve, reject (reason required) | `DecideApproval` |
| question | options, free answer, details later | `ReplyMessage` |
| escalation | its answers | `ReplyMessage` → `closeEscalation` |
| decision | value, alternative, own words, reject | decision record |
| review | accept, return (comment required) | review endpoints |

`POST /topics/{id}/decide` `{items: [{kind, ref, version, action, reason}]}` decides several items in **one
transaction** (all or nothing): "Approve all" sends every open approval of the topic; "Retry all" every escalation.
`DecideApproval` and `ReplyMessage` get `…Tx` forms for this. Per-item endpoints stay.

## D6. What Core adds

On read (`GET /topics`, `GET /topics/{id}`, the overview): project, task title and status, OpenSpec change, tasks
parked on the items (who waits), deadline and blocking, the chain (made by → brought by), for an artifact item the
previous version people saw with their decision and comment, and `includes` parts. Ids and hashes are returned for
links, never as text.

## D7. Text without JSON

Message bodies stay for agents. People's surfaces render the topic's brief, items' notes and Core context, and never
the body of an item that has a brief. For items without one (history, migration) Core serves `text`: the body with
the technical lines removed (`Exact artifact version: {…}`, `Exact call: {…}`, `Artifact approval <id>:`,
`Approval <id>:`, `Escalation <reason> on task <id>`). A call approval's `after` is generated per tool:
`catalog_change` lists the documents put and deleted, `project_create` names the project and repository mode,
commands show the command line.

## D8. Core templates

`escalationBrief(reason, task, summary) Brief` per reason of `escalationAnswers` (retries_exhausted,
budget_exhausted, change_missing, no_exit, dependency waits, session_lost, quota_paused, …), e.g.:

- title: "producer: Conversation of producer with owner stopped after 3 failed runs"
- ask: "Run it again or cancel it?"
- after: "retry: it runs again now (the cause below must be fixed first); cancel: the task closes"
- context: the cause in one line and the last error excerpt.

## D9. Chat and Inbox are one

A topic lives in the chat of the project's lead (`ownerChat(lead, project)`); the topic's first message is its card.
Messages with `topic_id` render in that chat as the topic thread. `message_send` and `message_reply` to people accept
`topic`; without it Core uses the open topic of the message's task. A person's reply in the thread (web chat, the
Inbox's reply box, a Telegram reply) is a message to the lead with `topic_id` and wakes its conversation task.
Read, answered and decided states are the stored ones, so the Inbox, the chat and Telegram agree. The badge counts
topics with open items plus topics with unread updates (inbox-badge-parity: the badge counts what the page lists).

## D10. Telegram and TUI

Telegram: every new open item, approvals included (today an escalated approval is not an attention event and never
reaches Telegram), sends one message: title, ask, recommendation, the item's images as an album, its buttons. Updates
appear in the digest.

Chat and Telegram are a log, the topic is live. A new version of an item posts a new short message in the topic's
thread ("kf2 v4 replaces v3: what changed", image, buttons). The older version's message is not deleted but retired:
the web shows "replaced by v4 →" without buttons; the bot edits its Telegram message (`editMessageText` and
`editMessageReplyMarkup`, new in `internal/notify/telegram/api.go`; the notifier keeps the sent message ids per item)
to drop the buttons and add the same line. A button pressed on a retired message is refused by Core, since the
approval is bound to the version, and the bot answers "replaced by v4". The topic card in the Inbox always shows the
current row; earlier versions and the person's comments are the row's history.

TUI: the inbox lists topics; Enter opens items and the thread.

## D11. Composites

`factory_artifact_register` and submission artifacts accept `includes: [{id, version}]` (same project, registered).
When a part's approval is rejected or a newer version of a part is requested, the composite's pending approvals are
superseded ("part kf2 v3 rejected"), using the existing superseded path. In a topic a composite with parts in the
topic is shown as the overview image and not offered as an item.

## D12. Batch escalation and version lines

`factory_approval_decide` takes `approval_ids` (with `approval_id` still accepted). `escalate` with several ids
requires each to be pending and delegated to the caller and goes through the gate once (one topic, items in order).

A new version of an artifact follows delegation like any request: it goes to the delegate (the lead). When the lead
escalates it, the gate finds the version line's people topic (open, or settled within 30 days, which reopens) and the
item joins it; the lead gives only `note.changes` (owner 2026-11-12: hierarchy and delegation are not bypassed).

## D13. `factory_people_brief`

`{topic_id, brief?, notes?: {item: ItemNote}, move?: [items], request_id}`: the topic's bringer, its manager chain or
the author of an item completes or restates the brief or notes, or moves items to a new topic. Clears `needs_brief`.

## D14. Timeout forwards

`AutoForward` moving a question to people has no bringer: the gate uses the author's brief if any, otherwise opens the
topic with `needs_brief` and Core's minimal brief (title from the task, ask from the question, "forwarded after Xh
without an answer from <manager>"), and sends the skipped manager a message to complete it with
`factory_people_brief`.

## D15. Language

A company setting `people_language` (default `en`; ilya-75ba24 `ru`). Briefs and notes are written in it: the tool
descriptions and the `brief_required` hint name it, and Core's templates (D8) exist in `ru` and `en`.

## D16. Open items are not migrated

Owner 2026-11-12: items open when this ships are not grouped or re-briefed; the owner decides them as they are. They
render as cleaned `text` (D7), without ids, hashes or JSON.

## D17. Who may bring items to people

The setting exists: an agent reaches people only with `owner` in `spec.communication.can_message` (checked today by
`owner_ask`, `owner_request_approval`, `artifact_request_approval`, `decision_propose`). Owner 2026-11-12: keep what
agents do now (a prompt that says "escalate to the owner" is fine), but let a company make delegation strict through
this setting. Changes:

- An agent without `owner` is **routed to its manager instead of refused**: its question goes to the manager as a
  question routed up the chain (the manager answers or forwards with a brief); its artifact approval that would go to
  people goes to its manager as the delegate. The answer says where it went.
- Its approval request with a delegate no longer needs `owner` (it never reached people).
- The gate checks the bringer: an item an agent brings to people (escalate, forward) needs `owner` in its
  `can_message`; without it the escalation goes one level up instead.
- The team page shows and edits it as "May bring questions and approvals to people" per agent.

## D18. Who decides an approval that moved

Found 2026-11-13: a delegate handed an approval up (D17), and `factory_approval_decide` refused its manager with "the
approval setting changed", because it recomputed the delegate from the project's mode; anything handed up got stuck.
Owner rule: the approval setting decides where an approval goes first (at request) and the ceiling; after that the
approval moves along the hierarchy, and the agent it is assigned to now (`delegate`, not escalated) decides it.

- `factory_approval_decide` authorizes the current holder, for yes/no and choice approvals alike.
- The ceiling is read at each decision from the current mode: people only (`owner`) refuses approve, reject, choose
  and changes from any agent; escalate (or a hand-up under strict delegation) stays allowed.
- A mode changed from one agent mode to another after the request leaves the approval with its holder, who still
  decides; lead mode keeps its `cites` rule for approve (since artifact-choice-approvals §5 every delegated mode has
  it: approving or choosing needs cites Core checks).
- A strict agent's approval that reaches its manager under people only says so in its setting: the manager brings it
  to people.

## Risks

- **Wrong grouping.** Version lines before tasks; the bringer can move items (D4).
- **Big topics.** Per-item buttons, collapsed items, "waits for you: N" in the header.
- **Urgency lost in a topic.** A blocking item raises its topic and shows in its header.
- **The lead is the broken one.** Core's items never wait for the lead (templates); D14 asks the manager, not blocks.
- **Free text acting.** Only `actions` act; thread text is conversation.
- **Agents learn the brief by refusals.** One refusal per path per agent, then the schema description; the
  `brief_required` rate per agent is in the feed.

## Not in this change

- Writing a topic's outcome to the decision journal (a lead may propose a decision deliberately).
- Changes to agent prompts (only with the owner's yes).
