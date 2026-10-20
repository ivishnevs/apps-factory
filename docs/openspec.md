# OpenSpec in Core storage

OpenSpec remains the source for product requirements and change history. Since core-owned-project-data
the project's OpenSpec tree lives in Core: every document version in Postgres (`spec_documents`: path,
version, sha256, size, who and when; the current version flagged, deletions kept as versions) with the
contents in Core's blob store by digest. Core never reads a node's disk, so Core and the nodes need no
shared filesystem; the project's `workspace` path is a node-side setting only (the nodes' shared-workspace
directory and writer-slot key) that Core never opens. Nodes materialize the tree into each run's worktree, agents use ordinary files and the
official OpenSpec CLI there, and the MCP adapter uploads their edits with compare-and-set on digests. Factory
stores task status, dependencies, attempts, messages and review in its database, plus a task's active
`openspec_change` name. This follows the official
[OpenSpec concepts](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md).

## Project setting

`openspec` in the project (`projects create`/`PATCH /projects/{id}`): `enabled` (default true), `dir`
(default `openspec`: where nodes materialize the tree in a worktree, relative, outside `.factory` and
`.git`) and `archive_assignee` (default: the manager of the change's last implementer). With
`enabled: false` the project has no tree in Core: reads, writes, checks and links are refused and runs
get no OpenSpec files.

Path rules are unchanged: `config.yaml`, Markdown and `.openspec.yaml` under `specs/` and `changes/`
(archive included), relative, no traversal, at most 1 MiB of UTF-8 per document. Other files in the
worktree's OpenSpec directory stay local and are never uploaded.

## Materialization and write-back

The claim answer's run spec carries `materialize`: `{openspec_dir, files: [{path, sha256, size}]}`, the
project's current tree plus the run's pinned approved artifact inputs (`.factory/inputs/<id>/<version>/<name>`,
see [artifact approvals](artifact-approvals.md)). Before the runtime starts the node downloads missing blobs
from `GET /runs/{id}/blobs/{sha256}` (node token or owner token plus the run's lease in `X-Factory-Lease`;
only blobs of the run's project), verifies and caches them per node by digest (`<state_dir>/blobs`), writes
them into the worktree, removes documents Core no longer has (other files of the directory stay), creates
`specs/` and `changes/archive/`, adds `/<dir>/` and `/.factory/` to the repository's `info/exclude` and
keeps the manifest in `.factory/manifest.json`. A blob that cannot be downloaded ends the run
`setup_failed` before the runtime starts.

Local edits a previous run left unsynced (it ended without an exit or `factory_openspec_sync`) are kept: a
document whose content differs from the digest the worktree's previous `.factory/manifest.json` recorded
(edited, deleted locally, or added locally) stays as it is, and the new manifest keeps that digest as its
base. Unmodified documents take Core's current version. The next exit uploads the kept edits with
compare-and-set against those bases, so a document Core changed meanwhile is refused as a conflict below.
Without a previous manifest for the same directory the worktree takes Core's tree.

Writes reach Core only with compare-and-set on the document's digest:

- `factory_openspec_write {path, base_sha256, content}`: `base_sha256` is the digest the caller's copy
  started from, empty for a new document; otherwise 409 `{"reason":"conflict","path","current_sha256"}`. A
  write of content that is already current succeeds unchanged (a retry never conflicts with itself).
- `factory_openspec_delete {path, base_sha256}`, `factory_openspec_move {from, to, base_sha256}`: a document
  with its digest, or a whole directory such as `changes/x` → `changes/archive/2026-10-20-x` without one;
  targets must not exist.
- Sync at exits: before every `factory_exit_*` (and before `factory_task_submit` without explicit exits) the
  adapter compares the worktree's OpenSpec directory with `.factory/manifest.json` and uploads changed, added
  and removed documents against the manifest digests, then records the new digests. A conflict refuses the
  exit (`openspec_conflict`) naming the paths; Core's current version is saved beside the file as
  `FILE.factory-current` and becomes the new base, so the agent merges, deletes that file and exits again.
  `factory_openspec_sync` does the same on demand. Documents the run did not change are never uploaded, so
  an unchanged stale copy cannot overwrite a newer version.

The owner writes with `POST /projects/{project}/openspec/write|move|delete` (same bodies and rules).

## Initialization, import and authoring

`factory openspec init PROJECT` (and `factory_openspec_init`) creates nothing: the tree's fixed directories
exist on every node; the answer lists them. The owner may call it any time, an agent only inside its active
run.

`factory openspec import PROJECT DIR` (owner CLI, run where the files are; DIR is the directory holding
`specs/` and `changes/`) uploads every OpenSpec document whose digest differs from Core's current version,
with compare-and-set on that digest, and reports written, unchanged and skipped (non-OpenSpec) files.
Nothing is deleted in Core; a repeated import changes nothing. Projects that track `openspec/` in git
conflict with materialization: import it, then stop tracking it (or set `openspec.dir`).

Use the installed official OpenSpec CLI on the worktree's copy to author proposals, designs, delta specs and
tasks; Core has no Node or OpenSpec subprocess dependency. Inspect its help before using commands; Factory
does not reproduce schema instructions, semantic validation, custom workflows or archive behavior:

```sh
openspec new change add-greeting --json
openspec validate add-greeting --strict --json --no-interactive
```

`factory_openspec_check` / API check validates a syntactically valid active change name and a nonempty UTF-8
proposal <= 1 MiB in Core's tree. The response says scope=reference; it does not claim semantic validity.

## Task and run links

Set `openspec_change` to an active change name such as add-greeting, not a path, archive name or URL. Create
its proposal before linking tasks. Create/update/ready/dispatch check the reference against Core's tree; claim
rechecks it: a missing/invalid queued reference ends that run `stale` and blocks its task before any runtime
starts. Restoring the proposal allows an explicit ready action and a new attempt. The node makes one final
preflight of the materialized proposal before starting the runtime; failure ends the run `setup_failed`.

The brief names the change and its directory; documents are not copied into RunSpecs, reports or decision
records. Changing Markdown checkboxes never changes Factory task status.

## Archive tasks and the daily check

When the last task linked to an active change becomes DONE, Core creates one READY task "Archive OpenSpec
change X" (linked to X, assigned to `openspec.archive_assignee`, else the manager of the last implementer,
else that implementer), once per change (`openspec_changes`). The assignee runs the official archive on its
worktree copy; its exit uploads the move below `changes/archive`. The daily check reports every active change
that no open task and no task closed within 7 days links: one `openspec.change_orphaned` feed event per
change, for the owner's default report.

## API, CLI and MCP

Owner endpoints require FACTORY_TOKEN:

- POST /projects/{project}/openspec/init with {"request_id":"bootstrap-1"}.
- GET /projects/{project}/openspec/list?dir=changes&after=NAME&limit=20.
- GET /projects/{project}/openspec/read?path=changes/add-greeting/proposal.md.
- GET /projects/{project}/openspec/check/add-greeting.
- GET /projects/{project}/openspec/manifest (every current document with digest and size).
- POST /projects/{project}/openspec/write|move|delete.

```sh
factory openspec init PROJECT
factory openspec list PROJECT changes 'limit=20'
factory openspec read PROJECT changes/add-greeting/proposal.md 'offset=8192&sha256=DIGEST'
factory openspec check PROJECT add-greeting
factory openspec manifest PROJECT
factory openspec import PROJECT ./openspec
```

MCP: factory_openspec_init/list/read/check/write/move/delete/sync (sync runs in the node's adapter).
Project, actor and run come from the run, never model arguments. Captured and current tool filters/policies
apply; denies prevail. Run tokens cannot use owner endpoints. Tool audit contains tool identity, request key
and result status, not document content.

Lists show one directory: the documents directly in it, subdirectories holding documents and the fixed
directories, sorted, with items/next_after/has_more (at most 2000 entries; limit defaults to 20 for HTTP and
10 for MCP, maximum 100/20). Reads return path/sha256/content/bytes/offset/next_offset/has_more on UTF-8
boundaries; default page 8192 bytes, HTTP max 32768, MCP max 8192. Continuation requires the prior sha256; a
changed digest returns 409: restart from offset zero.

The node omits document bodies from OpenSpec MCP read results in runtime telemetry and Core applies that
filter again before storing runtime events; this is not general DLP.

## Verification

`make check` covers storage-backed list/read/check/pagination and path rules, concurrent writes from one base
(one wins, the other gets 409 with the current digest), moves and deletes, the claim manifest and lease-bound
blob downloads, node materialization (tree, stale document removal, pinned artifact input, git exclude,
digest cache, a missing blob ending `setup_failed`, unsynced edits of a previous run kept and synced at the
next exit or refused as a conflict when Core changed the document), the adapter's sync at exits with an edited, an added and
a removed document and a conflict refusal, import of this repository's `openspec/` round-tripping digests,
archive task creation once per change and one orphan event per change.

`scripts/openspec-smoke.py --restart-core` is opt-in and consumes ChatGPT subscription usage. It runs
Manager -> linked Developer -> Review on a node of this machine, reads the tree only through the owner API,
edits the proposal in Core with compare-and-set (a stale base gets 409), validates a downloaded copy with the
official CLI, checks the review's checkbox edit arrived through its exit, the one archive task and omitted
document bodies in runtime telemetry. `--resume PROJECT` continues after inspection.
