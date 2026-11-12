## ADDED Requirements

### Requirement: One gate to people
Every item that becomes visible to people (created for them or moved to them: forward, timeout auto-forward, a
delegate's escalation) SHALL pass one Core gate, which assigns it to a topic and checks its brief. Agent-to-agent
items SHALL NOT need a brief.

#### Scenario: Lead decides alone
- **WHEN** ui-dev requests approval of an internal mockup and the producer approves it as the delegate
- **THEN** no brief SHALL be required and nothing SHALL reach people

### Requirement: Brief from whoever brings it
An item reaching people SHALL carry a brief written by its bringer (the author, or the agent that escalates or
forwards it), or by Core from a template when no agent brings it. A call missing a required field SHALL be refused
with `brief_required` naming the fields, and nothing SHALL be sent.

#### Scenario: Escalation without a brief
- **WHEN** the producer escalates an artifact approval to people without `brief.ask`
- **THEN** the call SHALL be refused with `brief_required` listing `ask`, and the approval SHALL stay with the producer

#### Scenario: Core's own escalation
- **WHEN** a task stops after three failed runs and its lead is the broken agent
- **THEN** people SHALL get Core's brief saying what stopped, what each answer does and the cause

### Requirement: Topics group what people decide
Core SHALL put each item into a topic by its artifact version line, then its task, then an explicit topic, and
Core's escalations by agent and reason. A topic SHALL open with a full brief; items joining it SHALL need only their
note. A new version of an artifact whose earlier version is in a people topic SHALL join that topic's row, with what
changed.

#### Scenario: Rework lands in the rejected row
- **WHEN** the owner rejected kf2 v3 in topic "P3-9a key frames" and artist-2d requests approval of kf2 v4 from another task
- **THEN** kf2 v4 SHALL appear in the same topic next to v3 with the owner's comment and the author's `changes`

#### Scenario: One broken agent, one topic
- **WHEN** four tasks of the producer escalate `retries_exhausted`
- **THEN** people SHALL see one topic with the four items and a "retry all" action

### Requirement: Actions come from the mechanics
People's buttons SHALL come only from each item's mechanics (approve a version, an option, an escalation answer), and
deciding several items of a topic at once SHALL decide each of them in one transaction. Text in a topic's thread SHALL
NOT decide anything.

#### Scenario: Approve all
- **WHEN** the owner presses "Approve all" in a topic with four pending artifact approvals
- **THEN** all four SHALL be approved together or, if one fails its check, none

### Requirement: No technical text for people
People's surfaces (web, Telegram, TUI) SHALL NOT show ids, hashes or JSON of an item; they SHALL show the brief, the
items' notes and Core's context (task, who waits, previous version).

#### Scenario: Legacy approval
- **WHEN** an approval created before this change is shown
- **THEN** its "Exact artifact version: {…}" line SHALL NOT be shown

### Requirement: Composites follow their parts
An artifact version that declares the versions it includes SHALL have its pending approvals superseded when any part
is rejected or replaced, and SHALL be shown as the topic's overview, not as an item to decide.

#### Scenario: Contact sheet
- **WHEN** the owner rejects kf1 v3 and the contact sheet includes kf1 v3
- **THEN** the contact sheet's approval SHALL be superseded

### Requirement: The topic is a chat thread
A topic SHALL live in the chat of the project's lead; the lead's updates and answers about it SHALL go to its thread,
and a person's reply in the chat, the Inbox or Telegram SHALL reach the lead in that thread. Read, answered and decided
states SHALL be the same on every surface.

#### Scenario: Follow-up question
- **WHEN** the owner asks "why a tap-to-play screen?" in the topic's thread
- **THEN** the producer SHALL get it in that topic, and its answer SHALL appear there and in the Inbox topic as unread

### Requirement: Open items are migrated
Open people items SHALL be grouped into topics when this ships; items without a brief SHALL be marked and their
lead asked once to complete the briefs. The decision journal SHALL NOT change.

#### Scenario: Pending approvals of 2026-11-12
- **WHEN** the change ships with 14 pending artifact approvals in Gnome Arena
- **THEN** they SHALL appear as topics by version line and task, and the producer SHALL get one message listing the topics to complete
