**NATS is out of scope: do not rework or remove it here. Known issue in `docs/backlog.md`.**

## 1. Keys and enrollment
- [x] 1.1 Node key pairs (Ed25519 sign, X25519 box) in `state_dir/keys/`, 0600 check at start and in doctor, fingerprint; mandatory policy refusing common commands that name the key files (guardrail, design Risks). Verify: unit tests for generation, reload, loose-mode refusal, stable fingerprint. Done: `internal/node/keys.go`, `node.keys` doctor check, start refused on a loose or damaged key (missing keys allowed until 4.2), builtin `node-keys` policy; `TestNodeKeys`, `TestFingerprint`, `TestKeysCheckMissing`, `TestDoctorChecksKeys`, `TestNodeKeysGuard`.
- [ ] 1.2 Enrollment codes (owner/admin route, operator for support, + `factory nodes enroll`), `POST /nodes/enroll` with rate limit, `GET /nodes/{id}/enrollment`, pending keys with a 24-hour limit, `factory nodes approve <node> <fingerprint>` and reject; `node_keys`, `node_enrollments` tables; globally unique 32-hex `node_id` (D2). Verify: expired, reused and wrong-company codes refused; a `node_id` used by another company or in another form refused; wrong fingerprint refused; pending node reaches only its enrollment status; the stolen-code scenario.
- [ ] 1.3 `factory-node enroll <code>` prints the fingerprint, waits for approval and stores the company from the answer; node.yaml `company` removed. Verify: node test against a fake Core.

## 2. Signed requests
- [ ] 2.1 RFC 9421 signing in all node clients: `API.Do`, `putLog`, `Fetch`, doctor; each attempt of a retry signs anew; the owner CLI stays unsigned. Verify: signature vectors round-trip with Core's verifier, also through a reverse proxy and with a log chunk at its largest size; a retried claim is not refused as a replay.
- [ ] 2.2 Core node gateway verifies signature, digest, ±120 s window, Core start time and nonce replay (240 s in memory), then the existing per-node route rules (the skill routes of `skill-registry` 3.4/4.3 plug into them). Verify: tests for unsigned, bad signature, tampered body, stale, replayed, created before Core started, revoked, other node's run; `TestNodeTokenAuth` replaced.
- [ ] 2.3 Doctor checks clock offset against Core's `Date` header.
- [ ] 2.4 Node routes as a marked class refused in the operator and owner branches (`internal/core/core.go:209`, `internal/core/companies.go:46`); `/health` stays open. Verify: operator, owner and old node tokens each get 401 on claim, credential and start; `internal/core/nodes_test.go`, `reconcile_test.go` and `internal/node/runner/v2_test.go` reworked.

## 3. NATS
Not in this change (see the top of this file).

## 4. Revocation, rotation, removal
- [ ] 4.1 `factory nodes revoke` (owner/admin), `factory-node rotate-keys` + `POST /nodes/{id}/keys` (signed by the current key, owner/admin approval, sequence of design D5), feed events (D6). Verify: rotation without the current key refused; old keys active until approval; after approval only the new keys work and the node continues without a restart; a live run is unaffected.
- [ ] 4.2 Remove node tokens (`node_tokens`, `/node-tokens`, `factory nodes token …`, `FACTORY_NODE_TOKEN`, `token_file`, service templates), the shared token on node routes, the v1 claim and worker path, `cmd/codex-worker` and `make worker`. Check which scripts in `scripts/` start nodes and update them. Verify: `go test ./...`; grep finds no remaining use.
- [ ] 4.3 (Waits for `user-accounts`: roles and `/c/{company}` pages.) Owner web nodes page of the company (`/c/{company}/nodes`, owner/admin): issue a code with the install command, pending nodes with fingerprints, approve/reject, revoke.
- [ ] 4.4 Revocation and `RemoveNode` delete keys, expire the node's run leases and release its pinned tasks in one transaction. Verify: run credentials of a revoked node's run are refused at once; a task pinned by unpushed work is released; a removed node has no active keys.

## 5. Docs
- [ ] 5.1 `docs/architecture.md`, `docs/development.md` (enrolling a node), `docs/runtime-protocol-v2.md` (signed requests).
