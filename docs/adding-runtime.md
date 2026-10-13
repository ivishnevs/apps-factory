# Adding a runtime

Keep RunSpec provider-neutral: identity, project/agent, workspace, prompt/instructions, timeout,
session ID and runtime_options. Core alone claims leases and updates persisted run state.

1. Register the provider in `protocol.RuntimeProvider.Valid` (currently codex and claude).
2. Implement a separate native process/service subscribing to factory.commands.agent.run.PROVIDER
   with its own shared durable consumer. Do not attach Claude to the Codex consumer.
3. Fetch authoritative RunSpec and obtain a Core lease before invoking the runtime.
4. Publish the same versioned started/progress/terminal events with correlation/causation IDs and lease.
   Persist opaque session ID via a started event; do not connect the Worker directly to SQLite.
5. Renew the lease, observe cancellation and classify unknown outcomes conservatively.
6. Request the run-scoped credential through /runs/{id}/credential with the lease and inject the
   provider-neutral factory-mcp stdio adapter. Remove owner/broker credentials from the runtime env.
   Do not implement task orchestration inside the runtime adapter.
7. Reuse workspace allocation and add runtime-specific smoke/integration tests. Keep provider settings
   inside runtime_options rather than adding SDK-specific fields to Core.

Claude is implemented as a TypeScript Worker in `workers/claude`; see claude-runtime.md.
Non-Go Workers reuse `bin/factory-workspace` (lock + OpenSpec preflight) and `bin/factory-mcp`.
Factory MCP instructions come from the credential response, not from Worker code. Task Engine creates the same durable
runs without a runtime SDK dependency. Preserve task_id, attempt and configuration snapshots
when adding providers; Core projects terminal run outcomes into task state transactionally.

Message delivery runs carry message_id without task_id. Preserve this context and let Core manage
blocking/answers and continuation. A provider must honor the same opaque session_id resume contract
for task continuations and owner conversations; no messaging scheduler belongs in a Worker.
