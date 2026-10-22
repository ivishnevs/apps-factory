## 1. Implementation
- [x] 1.1 Confined filesystem access, initialization, bounded listing/reads and reference checks.
- [x] 1.2 Task/dispatch/claim validation and native preflight.
- [x] 1.3 Owner API/CLI, scoped MCP and explicit Docker workspace mapping.
- [x] 1.4 Skill/prompt guidance and contract documentation.
- [x] 1.5 An archived linked change keeps its task runnable: ready, dispatch, claim and node preflight accept `changes/archive/YYYY-MM-DD-<name>/proposal.md`; create and link updates need an active change.

## 2. Verification
- [x] 2.1 Unit/race tests for confinement, permissions, idempotency and stale references.
- [x] 2.2 Build/vet and isolated JetStream integration.
- [x] 2.3 Real Docker/native Codex scenario, restart and retained state.
