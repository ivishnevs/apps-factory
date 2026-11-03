## Context
Nodes authenticate to Core with `Authorization: Bearer fnt_…` (Core keeps its SHA-256, `internal/storage/node_tokens.go`) and to NATS with per-node creds or an nkey seed issued by hand (`scripts/nats-secure-test.sh`). Both are bearer credentials stored on the node. Nodes run on the customers' machines (later on virtual machines we create for them); no agent runs on our hosts. A node serves one company and every team of it (`company-teams`). Enrollment, approval, revocation and rotation are done by the company's owner or admin (`user-accounts` D7); the operator token can do the same for support.

Threat this change addresses: **someone who is not the node pretends to be it** (stolen token, enrollment code, request capture, logs, proxy). Not addressed: a compromised running node (it holds its private keys and the env of its live runs) and a compromised Core.

## Goals / Non-Goals
Goals: possession of a node's private key is the only way to act as that node; no reusable credential travels on the wire or sits in Core's database; revocation is immediate on HTTP and NATS.
Non-goals: hardware-backed keys, mutual TLS (TLS terminates at the CapRover proxy), protecting a live compromised node.

## Decisions

### D1. Two key pairs per node
`factory-node enroll` generates the keys in `state_dir/keys/` (the only place they are created; a node without active keys refuses to run and asks for enrollment):
- `sign`: Ed25519. Signs HTTP requests and is the NATS user nkey (an nkey is an encoded Ed25519 key, so one key serves both).
- `box`: X25519. Used only to decrypt what Core seals for the node (`core-secret-store`).
Separate keys for signing and encryption, generated together, registered together. Private keys are 0600 files in `state_dir/keys/`; the node refuses to start when they are readable by group or others, and `factory node doctor` checks it. The **fingerprint** is the first 16 bytes of SHA-256 over both public keys, shown as hex groups.

### D2. Enrollment
1. An owner or admin issues an enrollment code for the company: web Nodes page or `factory nodes enroll --name mac-mini-1` (owner token; the operator token with `--company C` for support). The code is single-use, valid 15 minutes, stored as a hash, bound to the company and the intended `node_id`.
2. The node runs `factory-node enroll <code>`: generates keys (D1), sends `POST /nodes/enroll {code, node_id, sign_pub, box_pub}` signed by the new key, prints the fingerprint.
3. Core burns the code and stores the keys as **pending**. A pending node may only poll its status.
4. The owner or admin compares fingerprints on the web Nodes page (or `factory nodes approve <node_id> <fingerprint>`). Core marks the keys **active**; a wrong fingerprint is refused.
A code used by someone else first shows a fingerprint the owner does not recognise; the owner rejects it and issues a new code.

### D3. Signed requests
Every node-to-Core HTTP request carries RFC 9421 `Signature-Input` / `Signature` with `alg=ed25519`, `keyid=<node_id>`, covered components `@method`, `@path`, `@query`, `content-digest` (SHA-256 of the body, also sent), `created` and `nonce` (128-bit random). Core's node gateway:
- looks up the active `sign` key of `keyid` in the node's company; pending, revoked or unknown → 401;
- verifies the signature and the digest; `created` must be within ±120 s of Core's clock;
- rejects a `(node_id, nonce)` seen within the window (nonce table pruned after the window);
- then applies today's per-node route rules (`nodeGateway`: register, its heartbeat, claims naming it, run start/workspace/credential/heartbeat/project-setup, `GET /runs/{id}/blobs/…` and `PUT /runs/{id}/logs` of its runs, `/health`), plus `GET /skills/blobs/{hash}` for hashes pinned on its runs and the tree upload of a skill source sync job given to it (`skill-registry`).
The shared `FACTORY_TOKEN` and bearer node tokens no longer reach node routes.

### D4. NATS by node key
The node dials NATS with its `sign` key as nkey (`nats.Nkey`, the server's nonce challenge is signed by the private key). The NATS account uses an **auth callout** served by Core: Core receives the connecting nkey, checks it is the active key of a node, and returns a user JWT with permissions: fetch from `factory.<company>.commands.run.>`, subscribe to `factory.<company>.nodes.<node_id>.control`, nothing else. The callout's issuer key is operator configuration of Core (env, gopass). On revocation Core kicks the node's live connections through the system account. Static per-node users and passwords are removed.

The server does not verify the nkey before the callout: it passes the nkey, the signed nonce and the nonce in the authorization request. **Core SHALL verify that signature against the nonce and refuse a connection without a valid one**; a public key is not a secret, so checking only that the key is known would admit anyone. A test proves the server presents a nonce in our configuration and that a connection naming a node's public key without its private key is refused.

### D5. Revocation and rotation
- `factory nodes revoke <node_id>` (owner or admin; web or CLI): keys deleted, HTTP refused at once, NATS connection kicked, the node's runs follow the existing offline/lost rules.
- Rotation: `factory-node rotate-keys` generates new keys and sends `POST /nodes/{id}/keys {sign_pub, box_pub}` signed by the **current** key; the new keys are pending until an owner or admin approves the new fingerprint; the old keys stay active until then.
- Lost keys (reinstalled machine): revoke and enroll again.

### D6. Feed events
`node.enrolled` (pending, with fingerprint), `node.approved`, `node.enroll_rejected`, `node.keys_rotation_requested`, `node.keys_rotated`, `node.revoked`, `node.signature_refused` (rate-limited, with reason: unknown key, bad signature, stale, replay). Never key material.

### D7. Removal
Removed outright (unreleased): `node_tokens` table and routes, `factory nodes token create|list|revoke`, `FACTORY_NODE_TOKEN`, node.yaml `token_file`, NATS `creds_file`/password settings for nodes, the shared token on node routes, the v1 claim (`POST /runs/{id}/claim` without a body and node id: nothing can be sealed for an unnamed node) with the v1 worker path, `cmd/codex-worker`'s configuration through `FACTORY_TOKEN`. node.yaml gains nothing new except `state_dir/keys` by convention.

### D8. Virtual nodes later
A virtual node (e.g. a Fly Sprite) created by Core for a company through the provider's API uses the same keys, signatures and NATS authorization. Core issues the enrollment code into the machine's bootstrap itself, so the fingerprint is approved by the provisioning channel instead of by hand. Not part of this change.

## Risks / Trade-offs
- A private key file on disk can be copied together with the disk → encrypted disks (FileVault, LUKS) are recommended on node machines; Secure Enclave / TPM later.
- **Accepted in v1:** agents run as the node's OS user (`docs/policies.md`), so an agent with a shell or a prompt injection can read the node's private keys and the env and secret files of other live runs on that node, and could then act as the node until it is revoked. The goal of this change, someone outside who holds a token, a code or captured traffic, is still met, and the company boundary holds because a node serves one company. A mandatory policy refuses commands naming the keys directory as a guardrail only. Running agents under a separate OS user is a later change.
- Clock skew beyond 120 s refuses a node → doctor checks the offset against Core's `Date` header.
- The auth callout puts Core on the NATS connect path → a node that cannot be authorized retries; connected nodes are not affected by a Core restart.
