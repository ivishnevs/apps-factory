## 1. Engine

- [x] 1.1 Park kind `decision` (`decision_id`, optional deadline that escalates, never wakes to poll); recording or
      withdrawing the decision wakes every task parked on it with the outcome in the brief; tests.
      Done: `factory_exit_wait decision_id` (an 8+ character id prefix is accepted) parks with kind `decision`
      (migration 0032: park kinds, `topic`, `deadline`, `escalated_at`); approved, rejected or superseded (the only
      withdrawal path today) clears every such park with a `decision_recorded` trigger carrying the outcome;
      `under_discussion` wakes nobody. A decided or unknown decision is refused. The deadline opens one
      `event_wait_deadline` escalation to the owner (keep_waiting, continue, cancel). Tests:
      `internal/core/decision_waits_test.go`.
- [x] 1.2 Park kind `message` (`from` agent or owner, optional `topic`): the next message from that sender to the task
      or its agent in the project wakes it with the message in the brief; no timer polls; promised owner details
      (inbox-badge-parity 3.3) use it; tests.
      Done: `factory_exit_wait from` (+ `topic`); a message from that sender about the task (or about no task, to the
      assignee) in the project, an answer, or that sender's comment on the task clears it with `message_received`
      (the message is in the brief). `Store.ParkOnMessage(company, task, from, topic, reason)` is the function
      inbox-badge-parity 3.3 calls for "I'll send details" (not wired here). Deadline escalates to the owner when
      waiting on the owner, else to the assignee's manager.

## 2. Surfaces

- [x] 2.1 Decision card lists waiting tasks; board card "Waits for your decision: <topic>" with a link; "need you"
      counts it; dependents show the root.
      Done: `GET /decisions/{id}` and the list carry `waiting`; the web decision card (Inbox, chat) shows
      "TITLE and N more wait on this"; verdicts and task views carry `awaits` (also through dependencies, up to three
      levels, with the root `task_id`); the board says "Waits for your decision: TOPIC" linking to the Inbox notice;
      the park's clearer is the owner, so "needs you" counts it. The TUI shows the generic parked line.
- [x] 2.2 Park tool schema and descriptions, agent instructions; hint on a timed wait that names a proposed decision.
      Done: wait tool schema (`decision_id`, `from`, `topic`; `deadline` required only for a timed wait, by Core),
      run exit instructions and brief, `prompts/manager.md`, `presets/agents/developer/prompt.md`. A timed wait whose
      reason names an open decision is refused (`wait_on_decision`) with the `decision_id` hint (Core enforces, not
      only a hint).

## 3. Verify

- [x] 3.1 Replay VP-2: parked on the decision, no runs until the owner records it, then one run merges (approved) or
      reverts (rejected).
      Done with fakes: `TestDecisionWaitReplayVP2` (two days of ticks without a run, one run with "approved: …" in its
      brief that exits done) and `TestDecisionWaitRejected` (the rejection reason in the brief).
