# Design

Schema v6 adds artifact_versions and typed artifact approvals. Existing action grants retain
their scope and semantics. Submissions register references atomically; explicit MCP register
binds producer identity to the current task/run. Version URI changes are rejected.

Tasks carry required_artifacts. Ready/update/scheduler transactions project unsatisfied inputs
to an artifact wait block. Owner decisions reconcile eligible waiters atomically. Dispatch pins
approved version references and decision revisions in RunSpec/prompt; claim rechecks them.
Revoked queued work is cancelled before startup, preserving writer exclusivity. Running work
keeps its startup-authorized inputs; manual blocks and UNKNOWN remain untouched.

Inbox questions, versioned decisions, audit events and outbox reuse existing infrastructure.
Artifact grants are project-wide content decisions, never action permissions. Approved content
has no deadline; pending requests expire. Reconsideration creates a new explicit request/history.
The registry stores references; content addressing is the trusted producer's responsibility.
