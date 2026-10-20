## Context
`internal/openspec` opens a confined `os.Root` under the project's workspace (mapped into Docker Core), lists and reads documents with digest-pinned pagination, initializes directories and checks change references at create/update/ready/dispatch/claim; the worker repeats a local preflight. Artifacts are immutable `id/version/URI` references; "content addressing is the trusted producer's responsibility". Reports are rows in Postgres after `postgres-tenancy`.

## Goals / Non-Goals
Goals: Core deployable anywhere with no shared filesystem; one authoritative copy of specs and artifacts; agents keep using ordinary files and the official OpenSpec CLI on the node.
Non-goals: storing code, replacing git, real-time collaborative editing.

## Decisions

### D1. Storage
`BlobStore` interface (`Put(ctx, company, r) (sha256, size)`, `Get`, `Exists`), content-addressed per company; `localfs` implementation in a Core volume now, S3-compatible later. Tables: `blobs(company_id, sha256, size, created_at)`; `spec_documents(company_id, project_id, path, version, sha256, deleted, updated_by, updated_at)` with the current version per path and full history; artifact versions gain `sha256` and `size`; report attachments reference blobs. Small documents may be inlined but are still addressed by digest.

### D2. OpenSpec over Core storage
`internal/openspec` keeps its path rules (config.yaml, Markdown and `.openspec.yaml` under `specs/` and `changes/`, archive included; no traversal, bounded UTF-8) and its API/CLI/MCP shapes (`init`, `list`, `read` with digest pagination, `check`), now backed by `spec_documents`. Change-reference checks at create/update/ready/dispatch/claim query the database. Project setting `openspec.enabled` (default true) and `openspec.dir` (default `openspec`).

### D3. Materialization
The claim response includes `materialize: [{path, sha256, size}]` = the project's current OpenSpec tree plus the run's pinned approved artifact inputs (under `.factory/inputs/<id>/<version>/`). The node fetches missing blobs with the run token (`GET /runs/{id}/blobs/{sha256}`), caches them per node by digest, writes them into the worktree, adds the OpenSpec directory to `.git/info/exclude`, and stores the manifest in `.factory/manifest.json`. A failed download ends the run `setup_failed` before the runtime starts.

Unsynced edits are kept (owner decision 2026-10-20): a run that edited OpenSpec files and ended without an exit or `factory_openspec_sync` leaves them in the worktree with the previous manifest. At the next materialization a document whose content differs from the digest that manifest recorded (edited, deleted locally, or added locally without an entry) stays as it is, and the new manifest keeps that digest as its base; unmodified documents take Core's current version. The next exit (or sync) uploads them with the usual compare-and-set, so a document Core changed meanwhile takes the conflict path of D4. Without a previous manifest for the same directory the worktree simply takes Core's tree.

### D4. Write-back with compare-and-set
- `factory_openspec_write {path, base_sha256 | null, content}`, `factory_openspec_move {from, to, base_sha256}` (archive), `factory_openspec_delete {path, base_sha256}`: accepted only if the current digest equals `base_sha256` (null = must not exist); otherwise 409 with the current digest.
- **Sync at exits**: before committing any exit, the adapter compares the worktree's OpenSpec directory with `.factory/manifest.json` and uploads changed, added and removed files using the manifest digests as bases; a conflict refuses the exit with the conflicting paths (the agent re-reads and merges). `factory_openspec_sync` runs the same on demand.
- `factory_artifact_upload {id, version, path}`: the adapter streams the file from the worktree; the artifact version records `sha256` and URI `factory://artifacts/<id>/<version>`; owner approval binds to the digest. External URIs remain allowed as references without content.

### D5. Archive tasks
When OpenSpec is enabled and the last task linked to change X becomes DONE, Core creates one task "Archive OpenSpec change X" (idempotent per change) assigned per project policy (default: the manager of the last implementer), with the change linked. A daily check reports OpenSpec changes (not archived) that no open or recently closed task links, one feed event per change to the project's manager.

### D6. Run logs and attachments
Run logs (runtime event streams, stdout/stderr tails) and report attachments are uploaded by the node through the API with the run token (`PUT /runs/{id}/logs`, `factory_report_create` attachments → blobs), in chunks during the run and finally at the run end; Core never fetches them. Before upload the node redacts credential values it knows (environment secrets, tokens in the run's config, forge and runtime credentials) by exact-value replacement, plus the secret scanner's patterns; the run records that redaction was applied.

### D7. Removals and import
Removed: `FACTORY_WORKSPACE_HOST_ROOT`, `FACTORY_WORKSPACE_LOCAL_ROOT`, the Compose bind mount, Core-side `os.Root` access. The project `workspace` path stays (owner decision 2026-10-20) as a node-side setting only: the nodes' shared-workspace directory and the writer-slot key; Core stores and hands it to nodes but never opens it. `factory openspec import PROJECT DIR` (owner CLI, run on the machine that has the files) uploads an existing tree through the owner API with the same CAS rules.

## Risks / Trade-offs
- Specs no longer versioned in the project's git history → full history in `spec_documents`; a git mirror is an open question.
- Concurrent edits of the same document by two tasks → CAS on digests and conflict refusal at exit; agents merge.
- Projects that already track `openspec/` in git conflict with materialization → set `openspec.dir` or stop tracking it after import.

## Open Questions
- Should Core mirror approved specs into the project repository (read-only), and if so, who pushes (an agent task) given Core holds no git credentials? Owner decision 2026-10-20: not now; stays open.
- Resolved 2026-10-20: the project `workspace` path stays as the nodes' shared-workspace directory and writer-slot key (D7); unsynced OpenSpec edits survive the next materialization and are synced at the next exit (D3).
