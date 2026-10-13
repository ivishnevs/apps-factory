# Factory MCP verification — 2026-10-12

Environment: native macOS arm64, Go 1.24.1, Codex CLI 0.162.1 with existing ChatGPT login,
Docker Core and authenticated NATS JetStream. No API key or login mutation. No new dependencies.
Core data was backed up while stopped to .cache/mcp-backup/data before migration 2 → 3.

## Real planner → executor → reviewer

`scripts/mcp-smoke.py` passed using actual Codex app-server and required stdio Factory MCP.
Project: `mcp-smoke-739d85e9e2`.

| Stage | Agent | Task | Run | MCP calls | Final task state |
| --- | --- | --- | --- | --- | --- |
| Planner | manager | bc12adc78b564689599ce7efe1b38600 | e1ed812d56eb5f8ae4d694d9d83799c3 | 7 | DONE |
| Executor | demo | b66c5f1c9922b17d89ab1fba40f99a3e | a14aa6971465fe3ca0d730291f8636ec | 4 | DONE |
| Reviewer | manager | 7e057cda5c16a51796237934a458a675 | b98b7c8d089915d6667ad259e4a9b32d | 5 | DONE |

Planner read the project/current task, created both tasks and made them ready through MCP.
Executor read a real openspec resource, verified AGENT_CONFIG_OK and submitted summary
MCP_EXECUTOR_OK with a declared check through factory_task_submit. Reviewer read the task,
verified IN_REVIEW plus structured submission and accepted it through factory_task_accept.
Owner script accepted planner/reviewer tasks. No fixed workflow was added to Core or Worker.
All 16 tool calls returned 200; durable run/agent/project audit rows agree with task ownership.
Evidence is .cache/mcp-verification.json (local ignored file).

## Real thread resume

`scripts/smoke.py --mcp` passed. Both turns invoked factory_project_get and factory_skills_read
with their own run credentials and retained thread `01a1318c-c0b5-7fa3-845d-fe565f207f07`:

- `911e053f0daf4de40d214e2461328c98`: COMPLETED, FOUNDATION_OK, 51 events.
- `43718dc91e1cb352976b4273ee6a93a7`: COMPLETED, RESUME_OK, 48 events.

Results include model commentary before final markers. Evidence is .cache/mcp-resume-verification.jsonl.

## Automated checks

`make check` passes race-enabled unit tests and go vet. Tests cover JSON-RPC handshake/errors,
valid schemas, explicit bounded large output, scope/forged tokens, credential renewal lifecycle,
owner API rejection, actor/project injection, policy denial/approval, captured/current permissions,
assignment-only allowlist, skill confinement, stale versions, same-key replay, structured draft,
writer reservation, self-review denial, rework and Core restart with persistent credentials/audit.

Isolated NATS `go test -race ./internal/worker -run TestJetStream -v` passed:
Foundation (~2s), cancellation (~10s), Task Engine restart (~4s). Fake runtime tests are distinct
from the real MCP smoke above. The temporary integration broker was removed afterward.

Core/NATS remain running with durable state; native Worker is stopped after verification.
MCP tool content is capped at 64 KiB with explicit truncation; full domain data remains in Core.
Trusted host access remains intentional. Messaging, inbox, decisions, reports and approval grants
are outside this stage; policy approval requirements fail closed with 423.
