# Design

SQLite migration 7 adds immutable reports and versioned decisions. Core injects agent identity and reference scope. Significant reporting stays in the shared reporting policy. Core records observable terminal outcomes transactionally. Inbox notices are nonblocking.

One approved decision per normalized project/topic is enforced by a partial unique index. Replacement requires old ID/version and atomically preserves both records. Exact action grants protect agent decision recording. Decisions never become tool permissions. New runtime-neutral RunSpecs capture bounded approved context; old snapshots remain immutable. API/CLI and run-scoped MCP provide paginated reads and a short project feed.

See docs/reports-decisions.md for contracts and limits.
