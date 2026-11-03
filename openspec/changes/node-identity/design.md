## Context
Nodes authenticate to Core with `Authorization: Bearer fnt_…` (Core keeps its SHA-256, `internal/storage/node_tokens.go`) and to NATS with per-node creds or an nkey seed issued by hand (`scripts/nats-secure-test.sh`). Both are bearer credentials stored on the node. All nodes run on machines we operate; there is no self-hosted node.

Threat this change addresses: **someone who is not the node pretends to be it** (stolen token, enrollment code, request capture, logs, proxy). Not addressed: a compromised running node (it holds its private keys and the env of its live runs) and a compromised Core.

## Goals / Non-Goals
Goals: possession of a node's private key is the only way to act as that node; no reusable credential travels on the wire or sits in Core's database; revocation is immediate on HTTP and NATS.
Non-goals: hardware-backed keys, mutual TLS (TLS terminates at the CapRover proxy), protecting a live compromised node.

## Decisions

### D1. Two key pairs per node
On first start (no keys in `state_dir/keys/`) the node generates:
- `sign`: Ed25519. Signs HTTP requests and is the NATS user nkey (an nkey is an encoded Ed25519 key, so one key serves both).
- `box`: X25519. Used only to decrypt what Core seals for the node (`core-secret-store`).
Separate keys for signing and encryption, generated together, registered together. Private keys are 0600 files in `state_dir/keys/`; the node refuses to start when they are readable by group or others, and `factory node doctor` checks it. The **fingerprint** is the first 16 bytes of SHA-256 over both public keys, shown as hex groups.

### D2. Enrollment
1. The operator issues an enrollment code for a company: `factory nodes enroll --company C --name mac-mini-1` (operator routes). The code is single-use, valid 15 minutes, stored as a hash, bound to the company and the intended `node_id`.
2. The node runs `factory-node enroll <code>`: generates keys (D1), sends `POST /nodes/enroll {code, node_id, sign_pub, box_pub}` signed by the new key, prints the fingerprint.
3. Core burns the code and stores the keys as **pending**. A pending node may only poll its status.
4. The operator compares fingerprints and runs `factory nodes approve <node_id> <fingerprint>` (or the web nodes page). Core marks the keys **active**; a wrong fingerprint is refused.
A code used by someone else first shows a fingerprint the operator does not recognise; the operator rejects it and issues a new code.

### D3. Signed requests
Every node-to-Core HTTP request carries RFC 9421 `Signature-Input` / `Signature` with `alg=ed25519`, `keyid=<node_id>`, covered components `@method`, `@path`, `@query`, `content-digest` (SHA-256 of the body, also sent), `created` and `nonce` (128-bit random). Core's node gateway:
- looks up the active `sign` key of `keyid` in the node's company; pending, revoked or unknown → 401;
- verifies the signature and the digest; `created` must be within ±120 s of Core's clock;
- rejects a `(node_id, nonce)` seen within the window (nonce table pruned after the window);
- then applies today's per-node route rules (`nodeGateway`: register, its heartbeat, claims naming it, run start/workspace/credential/heartbeat/project-setup of its runs, `/health`).
The shared `FACTORY_TOKEN` and bearer node tokens no longer reach node routes.

### D4. NATS by node key
The node dials NATS with its `sign` key as nkey (`nats.Nkey`, the server's nonce challenge is signed by the private key). The NATS account uses an **auth callout** served by Core: Core receives the connecting nkey, checks it is the active key of a node, and returns a user JWT with permissions: fetch from `factory.<company>.commands.run.>`, subscribe to `factory.<company>.nodes.<node_id>.control`, nothing else. The callout's issuer key is operator configuration of Core (env, gopass). On revocation Core kicks the node's live connections through the system account. Static per-node users and passwords are removed.

### D5. Revocation and rotation
- `factory nodes revoke <node_id>` (operator, web): keys deleted, HTTP refused at once, NATS connection kicked, the node's runs follow the existing offline/lost rules.
- Rotation: `factory-node rotate-keys` generates new keys and sends `POST /nodes/{id}/keys {sign_pub, box_pub}` signed by the **current** key; the new keys are pending until the operator approves the new fingerprint; the old keys stay active until then.
- Lost keys (reinstalled machine): revoke and enroll again.

### D6. Feed events
`node.enrolled` (pending, with fingerprint), `node.approved`, `node.enroll_rejected`, `node.keys_rotation_requested`, `node.keys_rotated`, `node.revoked`, `node.signature_refused` (rate-limited, with reason: unknown key, bad signature, stale, replay). Never key material.

### D7. Removal
Removed outright (unreleased): `node_tokens` table and routes, `factory nodes token create|list|revoke`, `FACTORY_NODE_TOKEN`, node.yaml `token_file`, NATS `creds_file`/password settings for nodes, the shared token on node routes. node.yaml gains nothing new except `state_dir/keys` by convention.

## Risks / Trade-offs
- A private key file on disk can be copied together with the disk → encrypted disks are required on node hosts; Secure Enclave / TPM later.
- Clock skew beyond 120 s refuses a node → doctor checks the offset against Core's `Date` header.
- The auth callout puts Core on the NATS connect path → a node that cannot be authorized retries; connected nodes are not affected by a Core restart.
