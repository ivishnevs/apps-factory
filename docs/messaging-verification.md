# Messaging verification — 2026-10-12

Native macOS arm64, Go 1.24.1, Codex CLI 0.162.1 with existing ChatGPT login, Docker Core and
NATS JetStream. No dependency or API-key changes. Stopped Core data was backed up to
.cache/messaging-backup/data before SQLite migration 3 → 4; existing runs and MCP audit remain.

## Actual Codex scenarios

`scripts/messaging-smoke.py --restart-core` passed with actual required Factory MCP and live NATS.
Project: `messages-smoke-7e22650bd7`.

- Agent question: task `f28430829d507181b25abf2bb04e626f` asked manager through MCP, finished its
  turn, received an explicit manager reply in a separate run and resumed in the same task thread
  `01a131a0-5244-7b80-ae34-e150afbefcd6`. Two attempts, final AGENT_RESUMED_OK, accepted DONE.
  Recipient run: `170189b79d4c978ca5ab25c11e61f76c`.
- Owner inbox: task `65612a9ddcbdd25410b46719e9e87882` asked four structured questions, reached
  BLOCKED after its run completed, and waited without holding a worker. Core was restarted;
  question/form/wait state persisted. Owner answered all four form types through HTTP. Task resumed
  in thread `01a131a0-de04-7b52-8747-99e13d273673`, final OWNER_RESUMED_OK, two attempts, DONE.
- Owner dialog: conversation `da52cbd7bc919065bceb36ba277647ac` delivered two owner questions and
  explicit manager answers. Runs `a5a13de27599f97a5d7267d7c44cf222` and
  `948cb9fdcb5f73a47d4701b92f91a522` shared thread `01a131a1-28a2-7ff3-a81c-aff953d674b0`.
  Ordered history contained four messages.

Seven actual turns completed; two implementation tasks were accepted by the owner script. Responses
and question/reply MCP calls are persisted in Core. Local evidence: .cache/messaging-verification.json
and .cache/messaging-smoke-output.json. No messages were sent to external accounts/services.

## Automated verification

`make check` passes race-enabled Go tests and go vet. New cases cover:

- Atomic messages, state projections, answers and transactional outbox correlation.
- Reply before turn completion, waiting/restart/continuation, same task thread and writer exclusion.
- UNKNOWN/FAILED/CANCELLED never automatically resumed by an answer.
- Stable-key reply replay, stale versions, malformed forms/options and duplicate questions.
- Eight automatic clarification limit, persisted deadlines and owner escalation.
- Separate recipient contexts, persistent owner session and recipient/thread mismatch protection.
- Manager escalation → owner answer → manager continuation → originating task resume.
- Owner notification inbox acknowledgment.
- Scoped MCP sender/project injection, cross-project reads, can_message and policy approvals.
- Fresh snapshot decoding preserves independent captured/current permission checks.
- Missing recipient and oversized runtime context escalate instead of stopping the Core scheduler.

Isolated NATS race-enabled integration tests passed: Foundation (~2s), cancellation (~10s),
Task Engine restart (~4s). The temporary broker was removed. These fake-runtime regression tests
are separate from the actual messaging smoke above.

Core/NATS stay running with data. Native Worker is stopped after verification. HTTP/CLI inbox is
implemented; UI, external delivery, Decision Registry, reports and approval grants remain later stages.
