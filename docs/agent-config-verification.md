# Agent Config verification — 2026-10-12

- `make check`: all Go unit tests with race detector and go vet passed.
- Native factory/codex-worker and the Core Docker image built successfully.
- Real isolated JetStream integrations passed again (dispatch/result and heartbeat-driven cancellation).
- `factory config validate`: one agent, one profile, two policies, one registered skill.
- Resolved agent inherits role, openspec skill and engineering policies from developer profile.
- Release authorization returns approval from the mandatory system rule.
- HTTP tests enforce skill filtering, deny -> 403, approval -> 423 and resource confinement.
- Unit tests cover false/zero overrides, additive lists, allowlists, mandatory rules, approval inheritance,
  invalid/missing references, profile/policy cycles, lazy context and traversal/symlink rejection.
- Real Codex run `41dd674502fabf8c4209c635b0a220d4` completed with marker AGENT_CONFIG_OK.
  The marker existed only in skills/openspec/references/conventions.md, outside startup instructions.
  Persisted commandExecution events confirm use of `factory ... skills read`; 47 events were saved.
- Existing Foundation runs and results remain in the persistent database after Core image updates.

Core and NATS remain running. Temporary test broker removed; native demonstration Worker stopped.
Next stage: Task Engine. Factory MCP, parameter-bound approval grants and report persistence are
explicitly deferred, rather than represented as implemented by configuration metadata.
