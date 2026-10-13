## 1. Storage
- [ ] 1.1 `internal/blobstore` with `localfs`; `blobs`, `spec_documents`, artifact `sha256/size` migration; blob volume in `compose.yaml`. Verify: put/get round-trip and per-company isolation tests.
- [ ] 1.2 Back `internal/openspec` list/read/init/check by `spec_documents` with unchanged path rules and pagination. Verify: existing `internal/openspec` and `internal/core/openspec_test.go` cases pass without a filesystem.

## 2. Materialization and write-back
- [ ] 2.1 Manifest in the claim response; node download with digest cache; `.factory/manifest.json`; exclude the OpenSpec dir. Verify: node test materializes a tree and a pinned artifact; a missing blob ends `setup_failed`.
- [ ] 2.2 `factory_openspec_write/move/delete` with CAS. Verify: concurrent writes with the same base: one wins, the other gets 409 with the current digest.
- [ ] 2.3 Adapter sync at exits and `factory_openspec_sync`; conflict refuses the exit. Verify: adapter test with an edited, an added and a removed file.
- [ ] 2.4 `factory_artifact_upload`, digest-bound approvals, `factory://` URIs. Verify: approval of v1 does not satisfy v1 with different content.

## 3. Removal, import, archive
- [ ] 3.1 Remove bind mount, path mapping, Core workspace paths and Core-side file access. Verify: Core container runs with no host mounts; `make check` passes.
- [ ] 3.2 `factory openspec import PROJECT DIR`. Verify: import of this repository's `openspec/` round-trips digests.
- [ ] 3.3 Archive task creation when the last linked task is DONE; daily check for changes without a task. Verify: idempotent creation test; an orphan change yields one event.
- [ ] 3.5 Run log and attachment upload through the API with node-side redaction of known credential values. Verify: a planted token in a fake run's output is redacted in the stored log; Core makes no outbound request to the node.
- [ ] 3.4 Update `docs/openspec.md`, `docs/artifact-approvals.md`, `docs/architecture.md`, the OpenSpec skill. Verify: docs reviewed; `scripts/openspec-smoke.py` adapted and passing.
