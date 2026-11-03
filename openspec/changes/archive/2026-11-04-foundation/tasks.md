# Foundation tasks

- [x] Verify networking, install compatible Go NATS/SQLite/YAML dependencies.
- [x] Inspect Codex 0.162.1 official generated RPC schema and document version/limits.
- [x] Implement strict Foundation YAML configuration and an example agent/prompt.
- [x] Implement SQLite schema, atomic outbox, run claims, event deduplication and leases.
- [x] Implement authenticated Core HTTP API and native management CLI.
- [x] Implement file-backed JetStream streams and durable explicit-ack consumers.
- [x] Implement native Go Worker with official app-server stdio handshake/start/resume/events/interrupt.
- [x] Add workspace allocation interface and one-writer protection.
- [x] Add Docker Compose, native build commands and operational documentation.
- [x] Pass race/unit/static checks and real JetStream + fake runtime integrations.
- [x] Demonstrate real Codex start/resume, streaming and persisted results across Core restart.

Evidence: docs/foundation-verification.md. Later stages remain outside this change.
