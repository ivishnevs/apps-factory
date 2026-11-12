# People topics

Everything that reaches people (the company's owners and admins) — a question, an approval, a decision proposal,
work to review, Core's own escalation — passes one gate in Core. The gate puts the item into a **topic** and checks
its **brief**: what people read instead of the item's technical text. Agents deciding among themselves are not
affected. Change: `openspec/changes/people-topics`.

## The brief

| Field | What it says |
|---|---|
| `title` | one line, what this is about (≤ 120 characters) |
| `ask` | what people decide, naming exactly what is approved: the whole mockup, one element, one state (≤ 300) |
| `after` | what happens after each answer: who does what next |
| `recommendation` | the bringer's advice and why |
| `changes` | what changed since the version people saw |
| `scope` | optional: what people do not decide |
| `context` | optional: background |

An item joining a topic people already have open needs only its **note**: `{what, changes}`.

Who writes it: whoever brings the item to people. The author when it goes to people directly; the lead that escalates
an approval (`factory_approval_decide` with `action: escalate`) or forwards a question (`factory_question_forward`);
Core from its templates for its own escalations and alerts (English or Russian, company setting
`PUT /settings/company/people_language`).

What is required, by path:

| Path | Opening a topic | Joining one |
|---|---|---|
| Question (`factory_owner_ask`, `factory_exit_ask`, `factory_message_ask` to owner) | `title`, `after`; `recommendation` with options | nothing (the question is the item) |
| Forward to people | `title`, `ask`, `after`, `recommendation` | `note.what` |
| Call approval (`factory_owner_request_approval`) | `title`, `context`; Core writes `ask` and `after` from the call | `note.what` |
| Artifact approval to people / escalated | `title`, `ask`, `after`, `recommendation` | `note.what` |
| Decision proposal | `after` (title, ask and recommendation come from the topic, value and rationale) | nothing |
| Work to a person reviewer | `ask` (what to check) | nothing |
| Notification, report | — (joins the task's open topic as an update) | — |

Always: what changed (`brief.changes` or `note.changes`) for a version of an artifact whose earlier version people saw.

A missing field refuses the call before anything is stored:

```json
{"reason": "brief_required", "missing": ["ask", "after"], "joining": false,
 "hint": "This goes to people (the owner), so they read your brief instead of your text; nothing was sent. Add ..."}
```

The agent calls again with the brief and the same `request_id`; nothing was sent the first time, so nothing is
duplicated. The agent does not need to know in advance whether an item reaches people: the gate knows the recipient.

## Topics

A topic groups what people decide about one subject. Core finds an item's topic, first match:

1. an explicit `topic` (`people_topic` on `factory_decision_propose`);
2. the artifact's **version line**: a topic holding an earlier version of it, within 30 days (a settled topic
   reopens, so rework lands in the row people rejected);
3. Core's key for its own items: `agent:<assignee>:<reason>` (four `retries_exhausted` of one agent are one topic),
   `quota:<key>`;
4. the task's open topic (also a topic that holds the task's approvals or questions);
5. otherwise a new topic.

`factory_approval_decide` takes `approval_ids` to escalate several approvals with one brief: one topic, each item
named by its artifact and its author's words.

A topic is open while an item waits for people. Its items keep their own mechanics: an approval is still bound to
its exact version, an escalation answer still applies its effect. People decide per item, or several at once:
`POST /topics/{id}/decide` decides them in one transaction (all or none).

`factory_people_brief` lets the topic's bringer, the agents above it and its items' authors complete or restate the
brief, set item notes, or move items into a topic of their own. Core asks the skipped manager to complete a topic
when a question reached people after a timeout without anyone writing for them.

## Composites

`factory_artifact_register` and `factory_artifact_upload` take `includes: [{id, version}]` for a composite (a contact
sheet). Rejecting a part, or requesting approval of a newer version of a part, withdraws the composite's pending
approval.

## Strict delegation

An agent reaches people only with `owner` in `spec.communication.can_message`. Without it, its question to the owner
goes to its manager, its artifact approval that would reach people is its manager's to decide, and its escalation of
a delegated approval hands it one level up. Nothing is refused for that reason.

Who decides an approval that moved: the approval setting decides where an approval goes first and its ceiling. After
that the approval moves along the hierarchy, and the agent it is assigned to now (not escalated) decides it, whatever
agent the setting names today. Core reads the current mode at each decision: under people only (`owner`) no agent
decides, the holder only escalates (or hands it on, under strict delegation); a strict agent's approval that reaches
its manager under people only says so in its setting ("its manager brings it to them"). In every delegated mode the
holder's approval (choosing an option; for one version, approving it) needs `cites` Core checks
(artifact-choice-approvals).

## Surfaces

- Web Inbox: one card per open topic (brief, items with their notes, previews, buttons, approve all); the chat of the
  topic's lead shows its card and thread.
- Telegram: each item that waits is one message (brief, images as an album, buttons); an artifact approval has a
  button per option (Approve for one version) plus Changes… and Reject, a reply asks for changes and "reject:
  <reason>" rejects it (artifact-choice-approvals); a tool call's approval is approved with its button and rejected
  by replying with the reason; a replaced version's message loses its buttons.
- People never see ids, hashes or JSON: messages to people carry `text`, their body without technical lines.

## HTTP

| Route | |
|---|---|
| `GET /topics?project=&open=1` | topics with items, their status and actions, the tasks that wait |
| `GET /topics/{id}` | one topic |
| `POST /topics/{id}/decide` | `{items: [{kind, ref, version, action, reason?, answers?}], request_id}` |
| `POST /topics/{id}/messages` | `{body, request_id}`: a person writes in the topic thread to its lead |
| `GET/PUT /settings/company/people_language` | `en` or `ru` |
