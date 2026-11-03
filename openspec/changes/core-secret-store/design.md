## Context
`${secret:NAME}` references in `mcp_servers.yaml` travel unresolved in the config snapshot; the node resolves them from its `secrets_file`, passes values only through the runtime environment (`FACTORY_MCP_SECRET_<NAME>`) and redacts them (`internal/worker/external_mcp.go`). Nodes are always hosted by us; customers manage keys on the website. Claims are `POST /runs/{id}/claim` over HTTPS (TLS ends at the CapRover proxy); after `node-identity`, claims are signed and each node has an X25519 encryption key.

Threats addressed: stolen database or backup; someone pretending to be a node (`node-identity`); anyone reading traffic or logs between the proxy, Core and the node; a node keeping secrets it no longer needs. Accepted: a compromised running node sees the secrets of its live runs; a compromised Core sees all secrets (KMS narrows this later).

## Decisions

### D1. Key hierarchy
- **Master key**: 32 bytes from Core's env `FACTORY_SECRETS_KEY` (base64), backed up in gopass by the operator. Core refuses to start the secret routes without it. Accessed only through a `KeyProvider` interface (`Wrap`, `Unwrap`) so a KMS provider can replace it without touching callers.
- **Company key**: random 32 bytes per company, created with the company's first secret, stored wrapped by the master key in `company_keys(company_id, wrapped, key_version, created_at)`. Deleting a company deletes its key, which makes every copy of its secrets unreadable, backups included.
- **Secret value**: `AES-256-GCM(company key, random 96-bit nonce, value, AAD = company_id | name | version)` in `company_secrets(company_id, name, kind, version, nonce, ciphertext, updated_by, updated_at)`. The AAD makes a ciphertext moved to another row fail to decrypt.
- Master key rotation re-wraps company keys only (operator command, `factory secrets rotate-master`).

### D2. Owner surface
- Routes (owner token or operator): `PUT /secrets/{name}` `{kind, value}` (file values base64, at most 64 KiB), `GET /secrets` (name, kind, version, updated_by, updated_at, used_by: MCP servers and agents that reference it), `DELETE /secrets/{name}`. No route returns a value.
- CLI `factory secrets set NAME [--file path]` (value from stdin or file, never an argument), `list`, `delete`.
- Web: Secrets page (list, add/replace with a masked field or file upload, delete); the MCP server and agent views show which secrets they need and whether they are set.
- Names: `[A-Za-z_][A-Za-z0-9_]{0,127}` (`protocol.SecretRef`).

### D3. What a run needs
The set of names a run needs = `Secrets()` of the agent's MCP servers ∪ the names in the agent's `spec.secrets`. `spec.secrets` entries: `{name, env}` for a string (env var name, not `FACTORY_*`) or `{name, env, file: true}` for a file (the env var holds the file's path). Config loading validates names and env vars; a missing secret is reported by `factory agents check` and at claim.

### D4. Delivery sealed to the node
At claim (after `node-identity` verified the signature) Core decrypts the needed secrets and adds `secrets: [{name, kind, version, sealed}]` to the claim answer. `sealed` = HPKE (RFC 9180, `crypto/hpke`: DHKEM X25519, HKDF-SHA256, ChaCha20-Poly1305) to the claiming node's active encryption key with `info = company | run_id | node_id | name | version`, so a sealed value opens only on that node and only for that run. The claim answer is not stored; a needed secret that is not set fails the claim with `setup_failed` naming the secret, never a value. The go directive moves to 1.26 for `crypto/hpke`.

### D5. On the node
The node opens each sealed value with its private key, checks the `info` binding, and:
- MCP header secrets: `FACTORY_MCP_SECRET_<NAME>` in the runtime environment, header rewritten to `${FACTORY_MCP_SECRET_<NAME>}` (unchanged from `external-mcp-servers`).
- `spec.secrets` strings: the named env var in the runtime environment only.
- Files: `<run tmp>/secrets/<NAME>` mode 0600, path in the named env var; the directory is removed when the run ends and leftovers are removed at node start.
- Every value joins the run's redaction. Values are held only for the run and dropped when it ends; nothing is written to the node's disk except file secrets as above.

### D6. Audit
Feed events `secret.set`, `secret.deleted` (name, kind, version, actor) and `secret.delivered` (names, run, node). Never values.

### D7. Proof of no leaks
A test plants a known value as a secret, runs a fake-driver run that prints it, and scans every stored record (runs, snapshots, events, feed, run logs, fixtures, request logs) for the value and its base64; only the ciphertext row may hold it, and only encrypted.

### D8. Removal
`node.yaml` `secrets_file`, `internal/node/secrets.go` and the "Node secrets file" requirement are removed; resolution reads only the claim answer.

## Risks / Trade-offs
- A lost master key loses every secret → gopass backup is part of Core setup; startup refuses a key that cannot unwrap existing company keys.
- Core decrypts at every claim → negligible cost; no node-side cache (no sync, immediate revocation).
- A run that started before a rotation keeps the old value until it ends.
