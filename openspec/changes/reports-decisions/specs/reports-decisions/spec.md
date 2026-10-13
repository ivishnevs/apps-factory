# Requirements

- Significant reports persist purpose, changes, result, owner action and scoped references, with stable request retry and immutable content.
- Owner notifications must not block a task or confer approval.
- Observable terminal outcomes are automatically recorded and deduplicated; UNKNOWN reconciliation remains explicit.
- Proposals and approved decisions remain distinct; chat/task review cannot approve them.
- Concurrent decision updates use expected versions; replacement explicitly supersedes the current approved choice atomically.
- Agent decision recording requires exact owner authorization and cannot bypass denies.
- New runs receive bounded current approved context; existing run context remains unchanged.
- Owner API/CLI, scoped MCP and paginated project feed expose the durable journal across restart.
