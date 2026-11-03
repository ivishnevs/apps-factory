## Why
Credentials for agent tools (MCP server keys such as App Store Tracker, store and analytics keys) live only in a 0600 secrets file on each node (`external-mcp-servers`). Every node needs its own copy, Core cannot tell which node holds which key, and a customer should not edit a node file: nodes are the customers' own machines (later virtual machines we create for them), one company each, possibly several per company, and the customer enters keys once on the website.

## What Changes
- **Core keeps a per-company secret store**: a master key held outside the database (Core env now, KMS later behind the same interface) encrypts a random per-company key, which encrypts the company's secrets (AES-256-GCM, bound to company, name and version). Values are write-only through the owner API, CLI and web: reads return names, kind, version, who and when.
- **Secret kinds**: `string` (MCP header values, env values) and `file` (`.p8`, service-account JSON, keystores).
- **Delivery at claim**: Core resolves the secrets a run needs and puts them in that claim answer only, each **sealed to the claiming node's encryption key** (`node-identity`). Values are never part of the RunSpec, config snapshot, events, feed, run logs or fixtures. No node keeps secrets between runs, so rotation and deletion take effect on the next run without any sync.
- **On the node**: string values reach only the run's runtime environment, file values a 0600 file in the run's temp directory removed after the run; all values join run-log redaction.
- **Runs wait for missing secrets**: Core checks before dispatch that every secret a run needs is set; otherwise the task waits with a reason naming the secret and agent (no run, no retry spent) and is dispatched once the secret is set. The check at claim stays as a safety net.
- **Who**: owners and admins set and delete secrets (`user-accounts` roles); members see names only.
- **Agent secrets beyond MCP**: an agent may name secrets in `spec.secrets` to receive them as environment variables or files (e.g. an App Store Connect key for the `asc` CLI).
- **Audit**: every secret write and every delivery is a feed event naming the secret, run and node, never a value.
- **Removed** (no migration, not released): the node `secrets_file` (node.yaml setting, `internal/node/secrets.go`). Core is the only source of secrets.

## Out of scope
KMS (interface only), rotation reminders and expiry dates, browser-side encryption to node keys, importing secrets from elsewhere.

## Dependencies
`node-identity` (signed claims, node encryption keys), `external-mcp-servers` (`${secret:NAME}` references), `owner-web` (UI), `postgres-tenancy` (per-company storage).

## Capabilities
### New Capabilities
- `core-secret-store`: company secrets held encrypted by Core and delivered sealed to the claiming node.

### Modified Capabilities
- `external-mcp-servers`: secrets resolve from Core, not from a node file.

## Impact
`internal/storage` (`company_keys`, `company_secrets`), new `internal/secrets` (key provider, envelope encryption, sealing), `internal/core` (owner routes, claim answer, feed events), `internal/config` (`spec.secrets`), `internal/worker` (`external_mcp.go` takes sealed secrets, file secrets, redaction), `internal/node` (`secrets.go` and `secrets_file` removed), CLI `factory secrets set|list|delete`, owner web Secrets page, docs `agent-configuration.md`, `development.md`, `factory-mcp.md`, `runtime-protocol-v2.md`.
