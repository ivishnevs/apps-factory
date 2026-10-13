# Design
Core exposes a token-protected HTTP API and persists runs, events and outbox in SQLite WAL.
A unique active-project index enforces one writer; an UNKNOWN run quarantines its workspace.
Workers acquire an atomic Core lease before any runtime side effect. Delivery of a previously
claimed command cannot launch it again. Heartbeats renew leases; expiry marks UNKNOWN and
requires explicit owner reconciliation, never blind retry. Failed RPC/transport execution
is conservatively UNKNOWN; an explicit failed/completed/interrupted turn is known.

JetStream uses file-backed streams and durable explicit-ack pull consumers. Publication
uses event IDs as deduplication IDs. Core commits event plus state before acknowledging.
The outbox marks sent only after broker acknowledgement. Worker publishes events synchronously;
if publication fails, it stops the runtime and lease expiry preserves uncertainty.

Foundation deliberately supports a small, strict YAML subset; unsupported fields fail
validation rather than implying unenforced policies. Runtime-neutral RunSpec stores provider
settings in runtime_options. Codex-specific protocol stays inside the Worker.

Version gate: codex-cli 0.162.1, schemas generated with generate-json-schema.
API key auth is rejected; account/read must identify ChatGPT. No login is changed.
Unexpected server requests receive a JSON-RPC error and stop the run as UNKNOWN;
approval requests are declined, user-input requests interrupt and surface diagnostics.
