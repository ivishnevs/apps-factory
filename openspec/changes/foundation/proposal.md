# Foundation

Implement Go Core as the only SQLite owner, transactional dispatch through NATS JetStream,
validated YAML agents, and a native macOS Go Worker using Codex 0.162.1 app-server stdio RPC.
Demonstrate one run, streamed events, and persisted output. Use the owner's ChatGPT login.
Task engine, MCP, profiles, policies, messaging and dashboard remain later stages.

## Validation
Unit tests for configuration, storage transactions/idempotency/leases, and RPC framing;
integration test with real JetStream and a fake RPC process; opt-in real Codex smoke run.
