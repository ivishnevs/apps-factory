## Why
Core reads and writes project files through a bind mount (`FACTORY_WORKSPACE_HOST_ROOT` → `/workspaces`): OpenSpec documents, artifact checks and claim-time preflights all assume Core sees the same disk as the worker. That ties Core to the machine that runs agents, breaks as soon as there is more than one machine, and makes a hosted Core impossible. With per-task worktrees (`task-worktrees`) there is no longer one canonical project directory to mount anyway.

## What Changes
- **Core never reads node disks.** All project data other than code — OpenSpec documents, artifact contents, report attachments, large run logs — lives in Core storage: Postgres for metadata and small documents, a content-addressed **object store** (`BlobStore`: local volume now, S3-compatible later) for contents.
- **Materialization**: the claim response carries a manifest of the project's current OpenSpec tree and the run's approved artifact inputs; the node downloads them into the worktree before the runtime starts.
- **Write-back via MCP** with compare-and-set on content digests: `factory_openspec_write/move/delete`, automatic sync of the materialized tree at exits, `factory_artifact_upload` for artifact versions (approval binds to the content digest).
- **Run logs and attachments** are uploaded by nodes through the API, with known credential values redacted on the node.
- **Code stays in hosted git**; Core holds no clones and no forge keys.
- OpenSpec remains a per-project setting, **on by default**; when all tasks linked to a change are DONE, Core creates a task to archive it.
- The bind mount, host/container path mapping and project workspace paths in Core are removed.

## Out of scope
Mirroring specs into the project's git repository (open question), document search, a web editor.

## Dependencies
`postgres-tenancy`, `trigger-log-scheduling` (claim brief), `explicit-exits-authority` (exits, adapter), `task-worktrees` (worktrees), `node-agent` (node token, slots).

## Capabilities
### New Capabilities
- `project-data-storage`: Core-owned storage of specs, artifacts and attachments; materialization onto nodes; digest-checked write-back; OpenSpec archive tasks.

### Modified Capabilities
None (replaces the file-backed access described in the openspec-integration change and URI-only artifact references from artifact-approvals).

## Impact
New `internal/blobstore`; `internal/openspec` (storage-backed access, same path rules), `internal/storage` (`spec_documents`, `blobs`, artifact digests), `internal/core/openspec.go`, `artifacts.go`, `mcp.go` (new tools, blob endpoints for run tokens), `internal/node`/`internal/worker` (materialize, sync), `cmd/factory-workspace` (OpenSpec preflight against the materialized manifest instead of host files), `cmd/factory-mcp` (sync at exits, upload), `cmd/factory` (`openspec import`), `compose.yaml` (no bind mount, blob volume), docs `openspec.md`, `artifact-approvals.md`, `architecture.md`.
