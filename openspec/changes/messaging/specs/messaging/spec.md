# Requirements

- Persist messages and conversations with transactional outbox and mutation replay.
- Bind MCP sender/project/run to credentials; enforce target, project, policy and version checks.
- Support notification, question, answer, review_request, review_result and escalation messages.
- Schedule separate recipient runs; never share a developer task thread with its responder.
- Release a waiting task's writer only on known terminal evidence; resume in the same task thread.
- Never auto-resume UNKNOWN/FAILED/CANCELLED or manually blocked work on answer.
- Provide owner conversations, messages and inbox HTTP API plus usable CLI.
- Support text, single_choice, multiple_choice and confirmation question forms.
- Reject duplicate questions, bound automatic clarification chains, persist deadlines and escalate.
- Explicitly answer questions; final text is not an implicit answer or a task acceptance.
- Demonstrate actual agent answer/resume, owner answer across restart and persistent owner dialog.
