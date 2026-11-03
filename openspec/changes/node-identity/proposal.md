## Why
A node proves who it is with a bearer token (`fnt_…`, `node-agent` 1.1) that travels in every request and sits on the node's disk; NATS uses a per-node password or creds file the same way. Anyone who copies the token from a log, a backup, a proxy or a config can act as that node: claim its runs and receive the task, the project files (`Materialize`), and — once `core-secret-store` lands — the company's secrets. Nodes are always hosted by us in the cloud, so there is no self-hosted case to keep working and the bearer token can be replaced outright.

## What Changes
- **Node keys instead of a node token.** On first start a node generates two key pairs: an Ed25519 signing key (also its NATS nkey) and an X25519 encryption key. Private keys never leave the node; Core stores the public keys.
- **Enrollment** with a single-use, short-lived enrollment code issued by the operator for a company. The node sends the code with its public keys; Core shows the key fingerprint; the operator confirms it after comparing it with the fingerprint the node printed. Until confirmed, the node is pending and gets nothing.
- **Every node request is signed** (HTTP Message Signatures, RFC 9421, Ed25519) over method, path, body digest, creation time and a nonce. Core refuses unsigned, stale or replayed requests. A stolen token, code or captured request is worthless.
- **NATS by node key**: the node connects with its nkey; Core authorizes it through a NATS auth callout (approved, not revoked, permissions for its company's command subjects and its own control subject). Revocation disconnects it.
- **Revocation and key rotation**: revoking a node deletes its keys and refuses its requests at once. Rotation is a request signed by the current key plus operator confirmation; a lost key means a new enrollment.
- **Removed** (no migration, not released): node tokens (`node_tokens`, `/node-tokens` routes, `factory nodes token …`, `FACTORY_NODE_TOKEN`, node.yaml `token_file`), the shared `FACTORY_TOKEN` on node routes, NATS passwords and static per-node users.

## Out of scope
Keys in Secure Enclave / TPM (private keys are 0600 files on an encrypted disk for now), short-lived node certificates, the agent-facing run token.

## Dependencies
`node-agent` (registration, node routes, control subject), `postgres-tenancy` (company scope). `core-secret-store` builds on the encryption key.

## Capabilities
### New Capabilities
- `node-identity`: node key pairs, enrollment with operator confirmation, signed node requests, NATS authorization by node key, revocation and rotation.

### Modified Capabilities
- `node-agent`: the node authenticates with its keys, not a token.

## Impact
`internal/node` (key generation and storage, request signing, nkey NATS connection, `factory-node enroll`), `internal/core` (enrollment routes, signature verification in the node gateway, NATS auth callout), `internal/storage` (`node_keys`, `node_enrollments`, nonce window; `node_tokens` removed), `internal/bus` (nkey options), CLI (`factory nodes enroll|approve|revoke|rotate`), owner web nodes page, `scripts/nats-secure-test.sh`, docs `architecture.md`, `development.md`, `runtime-protocol-v2.md`.
