## Context
Core runs in Docker; Codex Worker runs natively. OpenSpec is product requirements and change history, while SQLite remains the task/runtime store.

## Goals / Non-Goals
Provide file-based initialization, reads and reference checking. Do not implement an OpenSpec workflow engine, duplicate document text in SQLite, or infer task completion from Markdown checkboxes.

## Decisions
Map an explicitly configured host workspace root to /workspaces in Docker. Confine document access with Go os.Root; allow only supported OpenSpec document paths, bounded regular UTF-8 files and SHA-pinned pagination. Reject symlinks for OpenSpec roots and files. Bootstrap directories only, preserving all existing files and schemas.

Validate a named active change with a nonempty proposal when attaching/readying/dispatching tasks and again at claim. A removed queued reference cancels the queued run and blocks its task before Codex startup. Store only the change name and metadata in tasks, RunSpecs and initialization receipts/events. Native Worker performs a final local preflight. A task whose own run archived its change (the final PR archives while the task is in review) keeps it: ready, dispatch, claim and preflight fall back to the latest `changes/archive/YYYY-MM-DD-<name>/proposal.md`; new links need an active change. Reads remain current and lazy; prompts contain paths, not copied documents.

Owner initialization is allowed only without an active or UNKNOWN writer; an agent may initialize only within its own active run. SQLite serializes initialization with scheduling. The shared policy evaluator and exact approvals apply to MCP operations.

## Risks / Trade-offs
Filesystem edits are not SQLite transactions. No snapshot is implied by a task link: documents remain current; reads expose their digest. Cross-process malicious file editing is outside trusted-host isolation, but confined file opens prevent traversal and escaping symlinks. Custom OpenSpec schemas remain usable; Factory validates a reference, not their workflow semantics.
