## 1. Keys and enrollment
- [ ] 1.1 Node key pairs (Ed25519 sign, X25519 box) in `state_dir/keys/`, 0600 check at start and in doctor, fingerprint. Verify: unit tests for generation, reload, loose-mode refusal, stable fingerprint.
- [ ] 1.2 Enrollment codes (owner/admin route, operator for support, + `factory nodes enroll`), `POST /nodes/enroll`, pending keys, `factory nodes approve <node> <fingerprint>` and reject; `node_keys`, `node_enrollments` tables. Verify: expired, reused and wrong-company codes refused; wrong fingerprint refused; pending node reaches no node route; the stolen-code scenario.
- [ ] 1.3 `factory-node enroll <code>` prints the fingerprint and waits for approval. Verify: node test against a fake Core.

## 2. Signed requests
- [ ] 2.1 RFC 9421 signing in the node's Core client (method, path, query, content-digest, created, nonce; Ed25519). Verify: signature vectors round-trip with Core's verifier.
- [ ] 2.2 Core node gateway verifies signature, digest, ±120 s window and nonce replay, then the existing per-node route rules (the skill routes of `skill-registry` 3.4/4.3 plug into them); bearer tokens no longer reach node routes. Verify: tests for unsigned, bad signature, tampered body, stale, replayed, revoked, other node's run; `TestNodeTokenAuth` replaced.
- [ ] 2.3 Doctor checks clock offset against Core's `Date` header.

## 3. NATS
- [ ] 3.1 Node connects with its signing key as nkey; Core serves the NATS auth callout (active node → permissions on its company's command subjects and its own control subject). Verify: `TestNodeControlSubjectIsPerNode` and `scripts/nats-secure-test.sh` reworked for callout users; pending and revoked keys refused.
- [ ] 3.2 Revocation kicks live NATS connections. Verify: revoked node disconnected, reconnect refused.

## 4. Revocation, rotation, removal
- [ ] 4.1 `factory nodes revoke` (owner/admin), `factory-node rotate-keys` + `POST /nodes/{id}/keys` (signed by the current key, owner/admin approval), feed events (D6). Verify: rotation without the current key refused; old keys active until approval.
- [ ] 4.2 Remove node tokens (`node_tokens`, `/node-tokens`, `factory nodes token …`, `FACTORY_NODE_TOKEN`, `token_file`), the shared token on node routes, NATS creds/passwords for nodes. Verify: `go test ./...`; grep finds no remaining use.
- [ ] 4.3 Owner web nodes page of the company (`/c/{company}/nodes`, owner/admin): issue a code with the install command, pending nodes with fingerprints, approve/reject, revoke.

## 5. Docs
- [ ] 5.1 `docs/architecture.md`, `docs/development.md` (enrolling a node), `docs/runtime-protocol-v2.md` (signed requests).
