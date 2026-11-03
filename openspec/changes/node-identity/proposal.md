## Why
A node proves who it is with a bearer token (`fnt_…`, `node-agent` 1.1) that travels in every request and sits on the node's disk; NATS uses a per-node password or creds file the same way. Anyone who copies the token from a log, a backup, a proxy or a config can act as that node: claim its runs and receive the task, the project files (`Materialize`), and — once `core-secret-store` lands — the company's secrets. Nodes are the customers' own machines (later virtual machines we create for them, such as Fly Sprites); no agent runs on our hosts. A node serves one company and every team of it (`company-teams`). Nothing is released, so the bearer token can be replaced outright.

## What Changes
- **Node keys instead of a node token.** On first start a node generates two key pairs: an Ed25519 signing key (also its NATS nkey) and an X25519 encryption key. Private keys never leave the node; Core stores the public keys.
- **Enrollment** with a single-use, short-lived enrollment code issued for the company by its owner or admin (web Nodes page or CLI; `user-accounts` roles). The node sends the code with its public keys; Core shows the key fingerprint; the owner or admin confirms it after comparing it with the fingerprint the node printed. Until confirmed, the node is pending and gets nothing.
- **Every node request is signed** (HTTP Message Signatures, RFC 9421, Ed25519) over method, path, body digest, creation time and a nonce. Core refuses unsigned, stale or replayed requests. A stolen token, code or captured request is worthless.
- **NATS by node key**: the node connects with its nkey; Core authorizes it through a NATS auth callout (approved, not revoked, permissions only for its company's consumers, acks and events, its own control subject and its own inbox). Revocation disconnects it.
- **Revocation and key rotation**: revoking a node (owner or admin) deletes its keys and refuses its requests at once. Rotation is a request signed by the current key plus confirmation by an owner or admin; a lost key means a new enrollment.
- **Node routes for skills**: the signed gateway also serves skill blobs pinned on the node's runs and uploads of private skill source trees for sync jobs given to the node (`skill-registry`).
- **Removed** (no migration, not released): node tokens (`node_tokens`, `/node-tokens` routes, `factory nodes token …`, `FACTORY_NODE_TOKEN`, node.yaml `token_file`), the shared `FACTORY_TOKEN` on node routes, NATS passwords and static per-node users.

## Out of scope
Keys in Secure Enclave / TPM (private keys are 0600 files on an encrypted disk for now), short-lived node certificates, the agent-facing run token, a node serving several companies, provisioning virtual nodes (Fly Sprites; later, D8).

## Dependencies
`node-agent` (registration, node routes, control subject), `postgres-tenancy` (company scope), `user-accounts` (owner and admin roles, company web pages: until it lands, enrollment works through the CLI and API with an owner token, and the web page waits). `core-secret-store` builds on the encryption key.

## Capabilities
### New Capabilities
- `node-identity`: node key pairs, enrollment with owner or admin confirmation, signed node requests, NATS authorization by node key, revocation and rotation.

### Modified Capabilities
- `node-agent`: the node authenticates with its keys, not a token.

## Impact
`internal/node` (key generation and storage, request signing, nkey NATS connection, `factory-node enroll`), `internal/core` (enrollment routes, signature verification in the node gateway, NATS auth callout), `internal/storage` (`node_keys`, `node_enrollments`, nonce window; `node_tokens` removed), `internal/bus` (nkey options), CLI (`factory nodes enroll|approve|revoke|rotate`), owner web nodes page of the company (`/c/{company}/nodes`), `scripts/nats-secure-test.sh`, docs `architecture.md`, `development.md`, `runtime-protocol-v2.md`.
