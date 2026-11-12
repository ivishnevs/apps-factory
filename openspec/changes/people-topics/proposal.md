Status: proposed 2026-11-12 by the owner; owner decisions recorded below (discussion of 2026-11-12). Not implemented.

## Why

What reaches people does not say what they decide. Seen on 2026-11-12 in ilya-75ba24 (Gnome Arena):

- **Approval cards are system text.** Artifact approval `cb610bda` (task `877fcce0`, aim-zone mockups) reads
  "Artifact approval cb610bda…: v2 (v1 superseded): re-render after producer review; ranged zone states unchanged in
  design. Exact artifact version: {"id":…,"sha256":…}" followed by "producer handed it to you: …". The owner: "I do not
  even understand what exactly I approve, the whole mockup or one feature on it".
- **Nobody in the chain writes for the person.** The author (ui-dev) writes `reason` for its lead ("v2: re-render").
  The lead (producer) escalates because the plan reserves the decision for the owner and adds why it is the owner's,
  not what to decide. Core concatenates both with the version JSON (`internal/storage/artifacts.go`
  `RequestArtifactApproval`, `EscalateApproval`).
- **One subject, many cards.** 14 approvals were pending: 5 for `877fcce0`, 5 for the P3-9b wireframes (`59cc37d8`),
  each its own card. Rework comes from other tasks (kf2 v4 from `a80996dc`, kf3 v3 from `ff435273`) and is not linked
  to the rejected version the owner saw.
- **Composites outlive their parts.** The P3-9a contact sheet (`p3-keyframes-contact` v3) is a collage of kf1–kf4. The
  owner rejected kf1, kf2 and kf3 and approved kf4; the contact sheet still waits, because nothing links it to its parts.
- **Core's own escalations read like logs.** "Escalation retries_exhausted on task conv-producer-gnome-arena …: last run
  setup_failed: invalid agent snapshot: json: cannot unmarshal …", once per task: four identical cards for one broken
  agent.

Grouping today is only deduplication: the same text from the same sender, one open escalation per task and reason,
older versions of the same artifact superseded, decisions superseded by topic.

## What Changes

- **One gate to people.** Everything that reaches the people's Inbox and chat goes through one Core function. It is
  the only way in, whether an item is created for people or moves to them (forward up the chain, the timeout
  auto-forward, a delegate's escalation).
- **One format for people.** At the gate an item gets a brief: title, what you decide, what happens after,
  recommendation, what changed since the version you saw, optional scope ("you do not decide"), context. Core adds the
  task, spec, who waits, deadline, the previous version with your earlier comment, and the chain (made by, brought by).
  People never see ids, hashes or JSON.
- **Whoever brings it to people writes the brief.** The agent tools that can reach people get one optional `brief`
  field (same schema everywhere). It is required only when the item goes to people; Core refuses the call with
  `brief_required` naming the missing fields, nothing sent, as with `kind` on tasks and `options` on questions.
  Agent-to-agent work (a lead deciding a delegated approval) is unchanged and needs no brief. Core writes the brief of
  its own items from templates.
- **Topics group what people decide.** A topic is the unit in the Inbox and a thread in the chat with the manager. Core
  puts an item into a topic by key: the artifact's version line, then the task, then an explicit `topic`; Core's own
  escalations by agent and reason. A topic opens with a full brief; an item joining it needs only its note.
- **Decide per item or all at once.** Buttons come only from Core, from the mechanics of each item (approve this
  version, option B, retry). "Approve all" runs the separate decisions. Free text in the thread never acts.
- **Rework lands where you rejected.** A new version follows delegation as usual; when the lead escalates it, it joins
  the row of its earlier version in the people topic, with only a `changes` note.
- **Composites follow their parts.** An artifact version may declare `includes`; rejecting or replacing any part
  withdraws it. In a topic it is shown as the overview, not as an item.
- **Batch escalation.** `factory_approval_decide escalate` takes several approvals and one brief.
- **Manager updates in the topic thread.** Answers to the person's follow-ups and status updates go to the topic's
  thread; the person replies there, in the Inbox or in Telegram; read and decided state is the same everywhere.
- **One new tool**, `factory_people_brief`: the bringer or its lead completes the brief of an open topic (timeout
  forwards, migrated items, a clarification asked in the thread).
- **Telegram carries topics too**, approvals with their images included; a replaced version's message is retired
  (buttons removed, "replaced by vN") instead of deleted.
- **Language for people**: a company setting; briefs and Core's templates use it.
- **No migration**: items open when this ships stay as they are, shown without ids, hashes or JSON.

Not changed: the internal types (approvals, decisions, escalations, questions) and their mechanics, agent-to-agent
flows, and the decision journal: a topic is not a decision and writes nothing to the journal.

## Owner decisions (2026-11-12)

1. Internal types stay (different inputs, effects and validations); people see one format in the Inbox and the chat.
2. The brief is written by whoever brings the item to people: Core when no agent is involved, otherwise the author or
   the manager who escalates.
3. Agent tools keep their names; the new fields are optional in the schema, required by the gate when the recipient
   is people, with an error saying what to fix.
4. Approvals are not collected into decisions; the journal holds only deliberate `decision_propose` outcomes.
5. No JSON, ids or hashes in the UI.
6. The manager's updates and clarifications come in the chat thread of the topic; answering there or in the Inbox is
   the same.
7. A new version follows delegation (the lead), it does not go to people directly; escalated, it joins its row.
8. The topic lives in the chat of the lead of the team that opened it.
9. No separate required scope field: `ask` must name exactly what is approved.
10. Briefs in the company's language for people (ru for ilya-75ba24).
11. Open items are not migrated; the owner answers them as they are.
12. Telegram sends topics with their images, approvals included; replaced versions' messages are retired.

## Impact

- Core: `internal/storage` (new `people.go` gate, `topics.go`, migration 0041), every people-bound send and transition,
  `internal/core` tool handlers, `internal/factorytools` schemas.
- Web: Inbox and chat render topics; TUI and Telegram render the same structured data.
- Docs: `docs/approvals.md`, `docs/artifact-approvals.md`, `docs/factory-mcp.md`, `docs/task-lifecycle.md`.
- Agent prompts need no change; lines in team prompts about the format of escalations may be aligned later, only with
  the owner's yes.
