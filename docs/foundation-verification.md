# Foundation verification — 2026-10-12

Environment: macOS arm64, Go 1.24.1, codex-cli 0.162.1, ChatGPT login,
Docker Desktop 29.8.2, Compose 5.6.0, NATS 2.11.4.

- Internet: Go module proxy returned HTTP 200; module downloads and Docker pulls succeeded.
- Build: native factory/codex-worker binaries and Linux Core Docker image built successfully.
- Unit checks: `go test -race ./...`, `go vet ./...` passed.
- Isolated real JetStream + fake app-server integration passed, including durable dispatch,
  SQLite result/events, duplicate event handling and cancellation via heartbeat/RPC interruption.
- Real Codex start: run `605503cdb856206b24667f70583db458`, COMPLETED,
  result `FOUNDATION_OK`, 28 persisted events including streamed agent message deltas.
- Real Codex resume: run `560bae003e0a74723971873fece98e63`, COMPLETED,
  result `RESUME_OK`, 30 persisted events including deltas.
- Both runs used thread `01a13143-d960-7613-8ae7-d580ef2c4b13`.
- Results and session IDs remained readable after Core restart and image replacement.
- API rejects unauthenticated access. Storage tests cover duplicate claims, lease expiry,
  no blind retry, queued cancellation, reconciliation and SQLite reopen.
- Workspace test confirms a second writer cannot acquire the lock until the first releases it.

Core and authenticated JetStream remain running in Compose with persistent volumes.
The temporary integration broker was removed. Native demonstration Worker was stopped after
verification; start `./bin/codex-worker` with the environment from README for further runs.
This verifies Foundation, not the later task engine/MCP/full MVP workflow.
