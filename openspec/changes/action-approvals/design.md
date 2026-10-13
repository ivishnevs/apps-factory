# Design

SQLite migration v5 introduces approvals with a unique scoped call fingerprint and inbox
message link. Shared transaction helpers create request+question+task wait and resolve
approval+explicit answer+task resumption atomically. Owner-only versioned/idempotent
endpoints approve, reject or revoke. Expiration closes pending questions without granting.

The MCP gateway checks both agent snapshot and current policy. Deny/capabilities remain
mandatory. Exact APPROVED intents permit downstream approval checks only through a
private request context; no HTTP parameter enables this. Gateway execution and owner
decisions are serialized in the single Core. Tool audit links authorized calls to grants.

Logical mutations retain their original request IDs; identical retries remain possible
until expiry/revocation. Unknown runtime outcomes retain existing quarantine behavior.
