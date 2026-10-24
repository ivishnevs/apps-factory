## Why
Credentials for agent tools (MCP server keys such as App Store Tracker, later store and analytics keys) live only on nodes today (`external-mcp-servers`: a 0600 secrets file per node). That keeps them off Core, but every node needs its own copy, Core cannot tell which node holds which key, and a SaaS user has no node file to edit: they will enter their keys on the website. Planned for the SaaS step (with sign-in, `owner-web`); not started.

## What Changes
- Core keeps a per-company secret store: values encrypted at rest with a key Core does not keep in its database (e.g. a KMS or an env-provided master key), write-only through the owner API and web UI (names, created/updated and who, never values back).
- `${secret:NAME}` references in MCP server definitions resolve in Core's store first, then in the node's file (the node file stays for self-hosted owners).
- The node receives resolved values only in the claim answer of a run that needs them, over the authenticated node API; they are never part of the stored RunSpec, config snapshot, events, feed, run logs or fixtures, and the node adds them to run-log redaction as today.
- Audit: every secret write and every claim that received a secret is a feed event naming the secret, never its value.
- Removes the node-placement gap: any node may run an agent whose secrets are in Core.

## Out of scope
Rotation reminders, per-agent scoping beyond "the agents whose MCP servers reference it", importing secrets from Paperclip.

## Dependencies
`external-mcp-servers` (secret references and node resolution), `owner-web` (UI), `postgres-tenancy` (per-company storage).

## Capabilities
### New Capabilities
- `core-secret-store`: company secrets held by Core and delivered to runs at claim.
