## Context

Parks (`task_parks`) already record who clears each wait: `message` parks hold the awaited sender in `ref_id`,
`awaiting_approval` parks the approval (its delegate, or people), `ask` and `escalation` parks the question's recipient
(`parkWho`). Core therefore already has the edges of a wait-for graph; it never reads them as a graph.

## Goals / Non-goals

- Goals: no two agents asleep on each other without anyone knowing; a refusal at the moment of the wrong wait, phrased
  so the agent fixes it in the same run; no extra runs or prompts in the normal case.
- Non-goals: general deadlock detection over dependencies and blocks (tasks that block on tasks already stay visible
  as `dependencies`), people's waits, cross-project waits.

## Decisions

### D1. The graph is agent-level

An edge is "task T of agent A waits on agent X" when T has an active park whose clearer is X (`message` from X, an
approval delegated to X and not escalated, a question to X). Agent X is *stuck* when it has no live or queued run in
the project and every open task of X that could answer is parked. A cycle is A → X → A with both stuck. Longer cycles
(A → X → Y → A) use the same walk with a depth limit of 4.

Agent level, not task level, because the hand-off is at agent level: a message to an agent may be answered from any of
its tasks or its conversation, and agents do not reliably file messages under the right task (the root of case 1).

Conversation runs count as live work: a conversation with an unread message from A queued or running means X is not
stuck.

### D2. The gate runs inside the exit transaction

`factory_exit_wait` with `from` or `approval_id` (and, for D4, a delegate's own wait) evaluates the edge it would add
against the active parks, under the company lock the exit already holds. No new tables. The refusal is an
`ExitRefusal` like `approval_not_holding`:

```
wait_cycle: ui-designer's task b5450975 waits for a message from you (since 21:05, its last message to you: 6b0c1e2f
"PR #438 head 9c2590bc: still CHANGES REQUESTED …"). Waiting on ui-designer now puts both tasks to sleep. Send
ui-designer what it waits for first (factory_message_send task_id=b5450975…), then wait.
```

```
wait_unasked: ui-designer has nothing pending from you: its last message (verdict, 18:43) is newer than your last
message to it. It does not know you wait. Send your request (factory_message_send recipient=ui-designer, with your task
id) and then wait; if ui-designer said it will write unprompted, exit again with expects_unprompted: true.
```

"Pending from A" means: the newest message between A and X in the project (either direction, any task) was sent by A,
or a message from A to X is unread. The override is per exit and recorded on the park (`unprompted`), so the sweep and
the board can show it.

### D3. Message wake routing

`messageArrived` adds one condition: when the message's task is not assigned to the recipient, it also wakes the
recipient's tasks parked on the sender, as a message filed under no task does. A message filed under the recipient's
own task wakes only that task, which keeps the "your turn to one of six parked tasks" behaviour.

### D4. Approval holds

An `awaiting_approval` park stays non-interruptible for authority input (the decision is the clearer), but a message
filed under the held task from the approval's delegate, or from an agent the delegate messaged about that task, adds a
`message_received` trigger and runs one turn. Exits other than wait on the approval, submit (rework on the same branch)
and messaging are refused during that turn with the existing hold hint. A delegate that waits on the requester, or asks
it a question, while the approval is pending gets `wait_cycle` with the hint "decide changes requested with what is
missing".

### D5. Sweep

The stall sweep (already per company, without the company lock, on snapshots) evaluates the D1 rule over active parks
every 10 minutes. For a cycle found and not woken in the last 24 h, each task in it gets a `wait_cycle` trigger whose
text names the other side and its last message; the feed shows `task.wait_cycle`. A cycle with the same members within
24 h escalates once (`wait_cycle` escalation) to the nearest common manager of the agents, else the project lead, else
the owner, with the answers `wake`, `reassign`, `cancel`.

## Risks / Trade-offs

- False `wait_unasked` when X promised in a conversation outside the project → the override covers it; the refusal
  text names the override.
- Agent-level edges may call a cycle where one of X's other tasks would answer later → X is only stuck when none of its
  tasks or conversations can run; then nothing would answer anyway.
- Extra turns for held approvals cost runs → bounded to messages from the delegate's side, filed under the held task.

## Migration

None. The gate starts with the release; parks made before it are only seen by the sweep.
