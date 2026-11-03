# Design

Migration 4 adds conversations, versioned messages and message_runs. Messages are authoritative
SQLite records with bounded NATS outbox notifications; conversation ID correlates message events.
Stable request keys replay mutations atomically; duplicate normalized questions in a conversation
or task are rejected. At most eight automatic questions per conversation/task are dispatched;
further questions and deadlines escalate to the owner. Repeated owner question text in one
conversation is also rejected with a new key; replay the original key or clarify the new question.

Separate delivery runs use the existing RunSpec/outbox/claim/lease and project writer invariants.
They carry message_id without task_id, so replies never finish the sender's task. Core schedules
message deliveries before READY tasks to unblock callers. Agent names/permissions come from the
existing YAML catalog; there is no fixed manager routing. Communication targets include reserved
owner and explicitly named agents. owner is not a valid agent name. Captured/current policy and
communication permissions are checked independently (decode snapshots into fresh structs).

Task wait_message_id persists a blocking question. The asking run remains IN_PROGRESS until it
finishes; a known COMPLETED turn projects to BLOCKED while unanswered or READY if already answered.
An answer to a message-blocked task makes it READY only if its prior run is known COMPLETED.
Dispatch includes the explicit answer, resumes the saved task thread, then clears the wait marker.
Writer reservation is held until runtime termination; UNKNOWN remains quarantined. FAILED,
CANCELLED, manual blocks or user cancellation never auto-resume merely because an answer arrives.

Answers are explicit message.reply mutations, not inferred from final text. Recipient runs that
end without answering escalate the original question. Nonblocking answers to agents schedule a
separate delivery run; blocking answers resume their task instead. Owner advice to a manager can
reopen its unanswered known-completed delivery and create a continuation; UNKNOWN/timeout
escalations require owner handling and do not reopen automatically. No delivery run is blindly retried.

Owner-created conversations retain the last known-completed manager thread; only owner-originated
deliveries to that conversation's configured agent resume it. Developer questions use independent
communication contexts. Conversation history and unanswered questions survive Core restart.

HTTP/CLI owner endpoints derive sender=owner and reject injected identity. Scoped MCP tools derive
project/agent/run and check project membership, can_message, policies, versions, state and forms.
Agent question/reply operations use the existing per-run tool audit. Inbox contains pending owner
questions, escalated questions and unread owner notifications; notifications may be acknowledged
without dismissing questions. Four simple form types are validated by Core. No external messaging
service, OS isolation or implicit approval is introduced.
