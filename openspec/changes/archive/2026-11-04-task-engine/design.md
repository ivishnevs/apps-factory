# Design

Migration 2 adds projects, tasks, dependencies, attempts, comments/artifacts, entity events and
mutation idempotency records; preserves migration-1 runs. Scheduling atomically reserves a task,
creates its run/attempt and publishes via outbox. One writer per project/workspace and one reserved
run per agent; unique current active task-run index prevents duplicate dispatch. READY expresses
intent: the scheduler additionally requires all prerequisites DONE and available slots.

Successful runtime completion is an implicit submission to IN_REVIEW (trusting agent reports).
Explicit submission can attach summary/commit/checks/artifact version references after completion.
Review accepts to DONE or rejects to REWORK with feedback; rework requires an explicit ready action.
Known worker-reported FAILED turns may retry within the snapshotted max_retries limit. Manual reconciliation never authorizes automatic retry. UNKNOWN blocks the task/workspace
until reconciliation; it is never retried automatically. Rework resumes only the same task's last
known thread, and new tasks start independent threads. Worker completion never accepts review.

All mutations use expected versions; optional request_id persists original responses and rejects
key reuse with different input. Task and project events/outbox commit with their mutations.
Core authenticates owner calls using the existing token. Named-agent operations additionally use
the shared policy evaluator and communication permissions; this is validation, not identity isolation.
Task Engine APIs/CLI precede Factory MCP. Protected approval-required operations remain blocked.
