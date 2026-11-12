# Security review of the public surface (2026-11)

`saas-release-readiness` 3.1, 2026-11-12. A code review of everything Core and the web serve to the internet:
routes with no credential, and routes that take a member, owner, owner-token, node or agent credential. Nothing was
tested against prod. Each fixed finding has a regression test.

**Scope:** the Core handler and its gateways (`internal/core/core.go` `Handler`), the web BFF and proxy
(`internal/web`, `cmd/factory/web.go`), sign-in (`/auth/*`, `internal/auth`, Dex config), invitations, node enrollment,
connection and signed node requests (`internal/nodesig`), agent run tokens and `/agent/*` tools, `/hooks/paddle`, the
Telegram webhook, blob, artifact, log and attachment uploads and downloads, `/install.sh` and `/downloads/*`, skill
sources (public fetch, private sync, uploads), image previews, the owner MCP, data export and deletion, and rate
limits.

**What was looked for:** authorization gaps (cross-company access by id, `X-Factory-Company`, role checks), IDOR,
SSRF, path traversal, injection (SQL, shell, git options), secrets in answers or logs, CSRF on cookie routes, webhook
signatures and replay, token lifetime, and resource exhaustion across tenants.

**Result:** 2 High, 4 Medium and 4 Low findings fixed. 14 Low or informational findings are open and listed
below. No High or Medium finding is open.

## Fixed

| # | Severity | Finding | Location | Fix and test |
|---|---|---|---|---|
| F1 | High | **An admin could become an owner through an owner token.** Owners and admins issue owner tokens, and the token gateway let every token reach every route. An admin's token could promote the admin to owner (`PATCH /memberships/{user}`), invite people, manage billing and rename the company. | `internal/core/companies.go` `ownerGateway` | The gateway applies `requiredRole` with the token's role. A token is admin, except the company's first token, issued by the operator (`created_by = operator`), which is owner. Test: `TestOwnerTokenActsAsAdmin`. |
| F2 | Medium | **One click on a connection link attached someone else's machine.** `factory-node login` gives anyone a `/connect/<id>` link. An admin who opened a link sent by an attacker connected the attacker's node with one click, and the node then got the company's runs, secrets and repositories. The page showed the code, so comparing it proved nothing. | `internal/core/node_keys.go`, `internal/storage/node_connects.go` `ApproveNodeConnect`, `web/src/app/connect/[id]/page.tsx` | Approving needs `{"code": "..."}` typed from the machine's terminal (case, spaces and dashes ignored). A wrong or missing code answers 409 `code_mismatch` and leaves the request pending. `GET /node-connects/{id}` no longer returns the code. The page asks for the code and warns. CLI: `factory nodes connect ID CODE`. Test: `TestNodeConnect`. |
| F3 | High | **One delegated approval froze every agent of every company.** `factory_approval_decide` took `approvalMu` while `/agent/call` already held it (Go mutexes are not reentrant). From then on every `/agent/call`, `/agent/exit`, command approval and owner approval decision hung until Core restarted. Any delegate agent triggered it by following the documented flow. | `internal/core/delegation.go` `approvalDecideTool` | The second lock is removed. Test: `TestDelegatedApprovalDecideReturns` hangs without the fix and passes with it. |
| F4 | Medium | **A public skill source could fill Core's disk and memory.** The size limit was checked only after go-git had cloned and checked out the whole repository. An admin of any company could point a source at a huge repository and sync it repeatedly, in parallel. | `internal/skills/fetch.go` `FetchPublic` | Every answer from the source's server is cut at `MaxFetchBytes` (256 MiB, `ErrFetchTooLarge`). At most 2 public fetches run at once across Core. Test: `TestFetchLimits`. |
| F5 | Medium | **One image preview could take about 0.5–1 GB of memory.** 64 Mpx was allowed, and a 16-bit PNG decodes at 8 bytes per pixel. Up to 16 attachments per message were decoded, under the agent-call lock, with no limit across companies. | `internal/preview/preview.go` | At most 40 Mpx is decoded; a larger image is shown as it is. One decode runs at a time in the process. Test: `TestRenderPixelBound`. |
| F6 | Medium | **No storage quota per company.** Customers run their own nodes. With a run token a tenant could upload 64 MiB blobs (`PUT /agent/blobs`) or log chunks (`PUT /runs/{id}/logs`, up to 100,000 per run) without limit, filling the blob volume all companies share. | `internal/storage/blobs.go` `PutBlob` | `Store.BlobQuota` caps a company's stored bytes; past it, 409 (`ErrQuota`). It is set with `FACTORY_COMPANY_STORAGE_GB`: unset means 20 GiB with billing on (the hosted service) and none on a self-hosted Core; 0 means none. Tests: `TestBlobQuota`, `TestBlobQuotaConfig`. Unreferenced blobs are still not swept (O12). |
| F7 | Low | **A node could post a finished sync job again.** A new tree on a `track` source then changed the company's skills with no one asking. | `internal/core/skill_sources.go` `syncJobResult` | A job that is not pending answers 409 `job_finished`. Test: `TestSkillSources`. |
| F8 | Low | **The node's `git clone` of a project had no `--` before the URL.** Core's repository check keeps a leading `-` out today; this is defence in depth against a bad Core. | `internal/worker/setup.go`, `internal/workspace/worktrees.go` | `--` before the URL, as private skill syncs already did. Existing clone tests pass. |
| F9 | Low | **Indexing followed symlinks in a cloned public source.** A link to `/` plus a subpath could make Core index its own files. | `internal/skills/fetch.go` | Every symlink in the checkout is removed before indexing. Test: `TestRemoveSymlinks`. |
| F10 | Low | **Blob and archive-entry downloads lacked `nosniff`.** | `internal/core/blobs.go` | `X-Content-Type-Options: nosniff`. The web already sent it with `CSP: sandbox`. |

## Open (Low and informational)

None of these blocks the release. Each is a hardening step or needs a design decision.

| # | Severity | Finding | Location | Suggested step |
|---|---|---|---|---|
| O1 | Low | **Forged `X-Forwarded-For` defeats per-address limits.** Rate limits and the address shown on a connection request take the first entry as given. Forging it gets past the per-address limits, but the overall limits still hold; this is known and written in `ratelimit.go`. | `internal/core/node_keys.go` `clientAddress` | Trust the header only from a configured proxy (Railway's), and take the right-most untrusted hop. |
| O2 | Low | **The node gateway reads up to 16 MiB (80 MiB for skill sync jobs) before checking the signature.** Anyone who knows a node id can send that much per request. Each refusal also writes one feed item per minute. | `internal/core/nodes.go` `nodeGateway` | Check `Signature-Input` and the created time before reading the body, and stream the digest. |
| O3 | Low | **`/agent/exit` skips the agent's catalog policy check.** The same exits through `/agent/call` are checked. | `internal/core/mcp.go`, `exits.go` | Run `allowed()` for the exit's tool definition in `exitRoute`. |
| O4 | Low | **Project ids are unique across all companies.** A 409 tells one tenant that another company has a project with that id. | `migrations/0001_baseline.sql` `UNIQUE (id)` | Scope the check to the company, or answer with a generic error. |
| O5 | Low | **An agent can register an artifact pointing at any blob of its company by sha256,** including another project's, and then read it. It must already know the digest. | `internal/core/artifacts.go`, `storage/artifacts.go` `RegisterArtifact` | Require that the blob was uploaded by the same run, or is already referenced in the project. |
| O6 | Low | **The owner MCP's confirmation code goes to the calling model, not the person.** A prompt-injected owner agent could confirm its own pause, secret change or node-key approval. | `internal/ownermcp/ownermcp.go` | Show the code out of band (TTY or stderr) for the person to type. |
| O7 | Low | **`POST /node-connects/{id}/reject` is not scoped to a company.** Any admin who knows a pending request's 128-bit id can cancel it. | `storage/node_connects.go` `RejectNodeConnect` | Accept, since the id is secret and the only effect is a cancel; or limit rejection to the approver's company. |
| O8 | Low | **Answers of 500 carry the internal error text** (database messages). | `internal/core/core.go` `reply` | Log the error and answer a generic message. Some handlers rely on the text today. |
| O9 | Info | **Token lifetimes.** Owner tokens never expire; they can be revoked and show when they were last used. Sessions slide and end at most 90 days after sign-in, and a role change applies on the next request. | `storage/owner_tokens.go`, `storage/users.go` | Optional expiry for owner tokens. |
| O10 | Info | **`DEX_MOCK_CONNECTOR=1` signs anyone in as a fixed test user.** | `deploy/dex/config.yaml` | Release checklist: never set it in production. |
| O11 | Info | **Without `FACTORY_CORE_URL`, `install.sh` and `/node-install` take Core's address from `Host` and `X-Forwarded-Host`.** Only the requester gets the changed script, and the host is checked against `hostPattern`. | `internal/core/node_connect.go` `nodeURL` | Set `FACTORY_CORE_URL` in production. |
| O12 | Low | **Blobs that nothing references are never deleted.** Retention removes only blobs of archived artifact versions. F6 bounds the total per company. | `internal/storage/blobs.go` | Sweep blobs older than N hours that nothing references. |
| O13 | Info | **Node nonces live in Core's memory.** A replay is possible across Core processes, and requests signed before Core started are refused. That is fine while one Core process serves. | `internal/core/nodes.go` | Store nonces in Postgres if Core runs several replicas. |
| O14 | Info | **Previews still render under the agent-call lock** (F5 bounds the time and memory). | `storage/visuals.go` | Render asynchronously. |

## Checked and in order

- **Sign-in:**
  - The OIDC login keeps state, nonce and PKCE in an HMAC-signed, HttpOnly cookie with a 10-minute lifetime.
  - The callback compares state in constant time and verifies the ID token and nonce.
  - The return path is local only (`auth.LocalPath`).
  - An unverified email never links to an existing user.
  - Logout needs the CSRF header.
- **Sessions:**
  - Session and owner-token secrets are stored hashed.
  - The session cookie is HttpOnly, Secure and SameSite=Lax.
  - Every state-changing session request needs `X-Factory-CSRF` equal to the CSRF cookie.
  - Every session request names its company in `X-Factory-Company` and gets 404 without a membership, then 403 without the route's role (`requiredRole`).
  - Members see secret names only.
  - Sessions never reach node-protocol or operator routes.
- **`X-Factory-Company`:**
  - An owner token naming another company is refused (403).
  - Only the operator token may name any company.
  - Nodes take the company from their key and ignore the header.
- **Invitations:**
  - The token is stored hashed and is single use (row lock).
  - It lasts 7 days and is revocable, for the admin or member role only.
  - Previews are rate limited.
  - Accepting needs a session and the CSRF header.
- **Data rights:**
  - `GET /company/export` and `DELETE /company` need a signed-in owner or the operator, with the company id typed to confirm.
  - Core's own company is never deleted.
  - `DELETE /me` needs the email typed.
  - Exports leave out token and code hashes.
- **Webhooks:**
  - **Paddle:** the HMAC is compared in constant time, the timestamp must be within ±5 minutes, and an event id is applied once.
  - **Telegram:** the webhook needs the per-company secret header (constant time), and an `update_id` is applied once.
- **Node identity:**
  - **Signatures:** Ed25519 over method, path, query and body digest, within ±120 s, one use per nonce, active key only.
  - **Scope:** a node reaches only its own `/nodes/{id}`, the runs it holds (`RunNode`) and its own sync jobs.
  - **Enrollment codes:** 128-bit, 15 minutes, single use, stored hashed, with proof of the key and fingerprint confirmation.
- **Agent run tokens:**
  - The token is an HMAC over company, run and lease, valid only while the run is claimed or running with a live lease.
  - It never reaches owner routes.
  - Tool arguments are checked against the run's project, and `actor` is forced to the run's agent.
- **Storage queries:**
  - Reads by id filter `company_id`.
  - The `WHERE id=?` statements act only on ids already loaded by a company-scoped query.
  - No SQL is built from input.
- **Uploads and archives:**
  - Digests must be 64 hex characters and company ids match a pattern, so blob paths cannot leave the store.
  - Archives and uploads: no `..`, no absolute paths, regular files only, file count and byte limits.
  - The node reads worktree files through `os.OpenRoot`.
- **SSRF:**
  - Public skill sources are https only, with no credentials.
  - The dial-time check refuses loopback, private, link-local (metadata) and CGNAT addresses, and redirects must stay https.
  - Private sources are fetched by the customer's node.
  - Previews fetch nothing.
- **Web BFF:**
  - Changes need `X-Factory-Web` (no plain cross-site form can set it), and Core checks CSRF and role.
  - The proxy passes only the session, CSRF and login cookies, and only to a fixed list of Core prefixes, without `..`.
  - Raw content is served with `CSP: sandbox` and `nosniff`.
  - Without sign-in configured, the web API answers 401 unless `FACTORY_WEB_LOCAL=1` on a loopback Core.
- **Downloads:** `/install.sh` and `/downloads/node/*` take file names from a strict pattern, and every public route is rate limited (2.3).
