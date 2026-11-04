## Context
Nodes authenticate to Core with `Authorization: Bearer fnt_…` (Core keeps its SHA-256, `internal/storage/node_tokens.go`). That token is stored on the node and travels in every request. Nodes run on the customers' machines (later on virtual machines we create for them); no agent runs on our hosts. A node serves one company and every team of it (`company-teams`). Enrollment, approval, revocation and rotation are done by the company's owner or admin (`user-accounts` D7); the operator token can do the same for support.

Threat this change addresses: **someone who is not the node pretends to be it to Core** (stolen token, enrollment code, request capture, logs, proxy). Not addressed: a compromised running node (it holds its private keys and the env of its live runs), a compromised Core, and NATS.

**NATS is not part of this change.** Nodes keep today's NATS connection and logins. Its known issue, which must be solved before a node of an external customer connects, is in `docs/backlog.md`. Do not rework or remove NATS here.

## Goals / Non-Goals
Goals: possession of a node's private key is the only way to act as that node towards Core; no reusable credential travels over HTTP or sits in Core's database; revocation is immediate on HTTP.
Non-goals: NATS authentication or isolation, hardware-backed keys, mutual TLS (TLS terminates at the CapRover proxy), protecting a live compromised node.

## Decisions

### D1. Two key pairs per node
`factory-node enroll` generates the keys in `state_dir/keys/` (the only place they are created; a node without active keys refuses to run and asks for enrollment):
- `sign`: Ed25519. Signs HTTP requests, and nothing else.
- `box`: X25519. Used only to decrypt what Core seals for the node (`core-secret-store`).
Private keys are 0600 files; the node refuses to start when they are readable by group or others, and `factory node doctor` checks it. The **fingerprint** is the first 16 bytes of SHA-256 over both public keys, shown as hex groups.

### D2. Node identity
`node_id` is the existing generated id (`internal/node/identity.go`, 32 hex characters). Enrollment accepts only that form and refuses an id used by any company, so a `node_id` names one node globally and a signature's `keyid` finds its company with no other input. `name` is a label for people. Enrollment answers the node's company; the node stores it in `state_dir` and uses it for its NATS subjects, and node.yaml `company` is removed.

### D3. Enrollment
1. An owner or admin issues an enrollment code for the company: web Nodes page or `factory nodes enroll --name mac-mini-1` (owner token; the operator token with `--company C` for support). The code has 128 random bits, is single-use, valid 15 minutes, stored as a hash, bound to the company and the name.
2. The node runs `factory-node enroll <code>`: generates keys, sends `POST /nodes/enroll {code, node_id, sign_pub, box_pub}` signed by the new key, prints the fingerprint. This route takes no token and is rate limited per address.
3. Core burns the code and stores the keys as **pending**. Pending keys that are not approved within 24 hours are deleted. A pending node may only call `GET /nodes/{id}/enrollment`, signed; every other node route answers 401.
4. The owner or admin compares fingerprints on the web Nodes page (or `factory nodes approve <node_id> <fingerprint>`). Core marks the keys **active**; a wrong fingerprint is refused.
A code used by someone else first shows a fingerprint the owner does not recognise; the owner rejects it and issues a new code.

### D4. Signed requests
Every node-to-Core HTTP request carries RFC 9421 `Signature-Input` / `Signature` with `alg=ed25519`, `keyid=<node_id>`, covered components `@method`, `@path`, `@query`, `content-digest` (SHA-256 of the body, also sent), `created` and `nonce` (128-bit random). All node clients sign: `API.Do`, `putLog` (`internal/worker/runlog.go`), `Fetch` (`internal/worker/materialize.go`) and doctor (`internal/node/remote.go`). The owner CLI shares `worker.API` and stays unsigned, with its owner token. Non-loopback Core URLs must be HTTPS.

Core's node gateway:
- looks up the active `sign` key of `keyid` globally; pending (outside D3), revoked or unknown → 401;
- verifies the signature and the digest; `created` must be within ±120 s of Core's clock and not before Core started;
- rejects a `(node_id, nonce)` already seen. Nonces stay in Core's memory for 240 s, the full span of the window; the start-time rule covers a restart. Several Core instances will need shared storage;
- then applies the per-node route rules (`nodeGateway`: register, its heartbeat, claims naming it, `POST /nodes/{id}/keys`, run start/workspace/credential/heartbeat/project-setup, `GET /runs/{id}/blobs/…` and `PUT /runs/{id}/logs` of its runs), plus `GET /skills/blobs/{hash}` for hashes pinned on its runs and the tree upload of a skill source sync job given to it (`skill-registry`).

**Node routes are a marked class that bearer tokens cannot reach.** Today the operator and owner branches pass any route to the mux (`internal/core/core.go:209`, `internal/core/companies.go:46`); both refuse node routes with 401. `/health` stays open to signed nodes and to tokens.

### D5. Revocation and rotation
- `factory nodes revoke <node_id>` (owner or admin; web or CLI): keys deleted, HTTP refused at once. In the same transaction the node's run leases expire, so run credentials stop working, and its runs follow the existing lost rules, including the release of tasks pinned by unpushed work, as `RemoveNode` does. `RemoveNode` deletes keys too. NATS logins are not revoked by this (`docs/backlog.md`).
- Rotation: `factory-node rotate-keys` writes new keys to `state_dir/keys/next/` and sends `POST /nodes/{id}/keys {sign_pub, box_pub}` signed by the **current** key. The new keys are pending until an owner or admin approves the new fingerprint; the old keys stay active until then. The node polls the enrollment route; on approval Core accepts only the new keys, and the node moves `next/` into place and continues. Live runs are not affected, because their secrets are already open. A refused or expired request deletes `next/`.
- Lost keys (reinstalled machine): revoke and enroll again.

### D6. Feed events
`node.enrolled` (pending, with fingerprint), `node.approved`, `node.enroll_rejected`, `node.keys_rotation_requested`, `node.keys_rotated`, `node.revoked`, and `node.signature_refused` (rate-limited, with reason: bad signature, stale, replay) in the node's company. An unknown key has no company, so it is only logged. Never key material.

### D7. Removal
Removed outright (unreleased): `node_tokens` table and routes, `factory nodes token create|list|revoke`, `FACTORY_NODE_TOKEN`, node.yaml `token_file` and `company`, the shared token on node routes, tokens in service templates (`cmd/factory-node/main.go`, `internal/node/systemd.go`), the v1 claim (`POST /runs/{id}/claim` without a body and node id: nothing can be sealed for an unnamed node) with the v1 worker path, `cmd/codex-worker` and `make worker` (use `factory-node run`). NATS settings stay.

### D8. Virtual nodes later
A virtual node (e.g. a Fly Sprite) created by Core for a company through the provider's API uses the same keys and signatures. Core issues the enrollment code into the machine's bootstrap itself, so the fingerprint is approved by the provisioning channel instead of by hand. Not part of this change.

## Risks / Trade-offs
- **Known issue, must be solved before the first external node:** a holder of a NATS login can damage all companies' queues, and revoking a node does not cut its NATS login (`docs/backlog.md`).
- A private key file on disk can be copied together with the disk → encrypted disks (FileVault, LUKS) are recommended on node machines; Secure Enclave / TPM later.
- **Accepted in v1:** agents run as the node's OS user (`docs/policies.md`), so an agent with a shell or a prompt injection can read the node's private keys and the env and secret files of other live runs on that node, and could then act as the node until it is revoked. The goal of this change, someone outside who holds a token, a code or captured traffic, is still met, and the company boundary holds because a node serves one company. A mandatory policy refuses common commands naming the key files as a guardrail only. Running agents under a separate OS user is a later change.
- Clock skew beyond 120 s refuses a node → doctor checks the offset against Core's `Date` header.
- A proxy that rewrites the path breaks signatures → tested through a reverse proxy; CapRover is checked at the first deployment.
