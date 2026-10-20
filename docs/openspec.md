# File-based OpenSpec integration

OpenSpec remains the source for product requirements and change history. Factory stores task status,
dependencies, attempts, messages and review in SQLite, plus a task's active `openspec_change` name.
No document table or embedded document snapshot is added. This stage follows the official
[OpenSpec concepts](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md).

## Workspace access

Worker uses the absolute native host workspace from project metadata. Docker Core maps
FACTORY_WORKSPACE_HOST_ROOT to FACTORY_WORKSPACE_LOCAL_ROOT=/workspaces. Compose bind-mounts the
same host root; the installed .env pins it to this repository. For a different projects directory,
set FACTORY_WORKSPACE_HOST_ROOT to that absolute path and recreate Core. Without an explicit
setting Compose uses PWD; launch from the repository root. Project workspaces used by this
integration must exist beneath that root. Unlinked tasks/direct runs can still use their existing
Worker paths outside the mount. Native Core needs no mapping when both variables are unset.
Never put a container path into project metadata: Worker still needs the native path.

The mount is writable for initialization. Docker Core remains nonroot. Host filesystem permissions
must permit its configured container UID to create directories; Linux deployments should provision
compatible ownership/ACLs for the shared workspace. Docker Desktop's host sharing was verified
with the existing native directories. Factory does not chmod/chown an arbitrary project tree.

Document operations open a confined Go os.Root under the project's OpenSpec tree. Absolute paths,
traversal, escaping symlinks, symlink roots/final documents, devices and nonregular files are
rejected. Supported reads: config.yaml, Markdown and .openspec.yaml under specs/changes, including
archive history. Paths are relative to openspec. These controls protect the Factory gateway; native
Codex still has trusted host access, and owner-configured workspace aliases are not OS isolation.

## Initialization and authoring

`factory openspec init PROJECT` creates missing directories only:

```text
openspec/
  specs/
  changes/
    archive/
```

It does not generate config, instructions or product documents, and preserves existing files and
custom schemas. Workspace must already exist. Repeated initialization is safe; the API also accepts
a stable request_id for exact replay. Owner initialization requires no queued/running/cancelling or
unreleased lost writer for the project/workspace. An agent may initialize inside its own active run.
The SQLite transaction serializes these mutations against scheduling; a lost run whose runtime may still run
keeps its writer slot. Events and mutation receipts contain directory metadata only.

Use the installed official OpenSpec CLI/workflow or normal workspace file tools to author proposals,
designs, delta specs and tasks. CLI is needed only on the native authoring host; Core has no Node or
OpenSpec subprocess dependency. Inspected CLI for this installation: OpenSpec 1.14.0. Inspect its help
before using commands; Factory does not reproduce schema instructions, semantic validation, stores,
custom workflows or archive behavior. For example, the inspected commands include:

```sh
openspec new change add-greeting --json
openspec validate add-greeting --strict --json --no-interactive
```

`factory_openspec_check` / API check validates a syntactically valid active change name, a real
change directory and a nonempty UTF-8 proposal <=1 MiB. The response explicitly says scope=reference.
It does not claim that a proposal, requirement scenario or custom schema is semantically valid.
Design/tasks may be authored later; this avoids imposing a second workflow engine.

## Task and run links

Set `openspec_change` to a name such as add-greeting, not an absolute/relative path, archive name or
URL. Create its proposal before linking tasks. Create/update/ready/dispatch validate the reference.
Dispatch copies the name into RunSpec and adds a path/read instruction to the task prompt without
copying documents. Claim rechecks the file: a missing/invalid queued reference cancels that run and
blocks its task before Codex starts. Core records the observed CANCELLED outcome. Restoring the
proposal allows an explicit ready action and a new attempt. Native Worker makes one final local
preflight before starting app-server; failure publishes known FAILED evidence, without a session
or runtime side effects. Bounded retries with broken links become blocked instead of crashing Core.

Existing runs retain their name; documents remain current files, not frozen approved content.
Use versioned artifact approvals when exact immutable inputs are required. Agent instructions and
the shared OpenSpec skill require reading relevant specs and the active change before implementation.
Changing Markdown checkboxes never changes Factory task status; successful runtime evidence and
explicit task review still apply. Old terminal task links are retained as historical metadata.
Changes should be archived only after associated work is complete; an archived name cannot be used
for new active tasks.

## API, CLI and MCP

Owner endpoints require FACTORY_TOKEN:

- POST /projects/{project}/openspec/init with {"request_id":"bootstrap-1"}.
- GET /projects/{project}/openspec/list?dir=changes&after=NAME&limit=20.
- GET /projects/{project}/openspec/read?path=changes/add-greeting/proposal.md.
- GET /projects/{project}/openspec/check/add-greeting.

```sh
factory openspec init PROJECT
factory openspec list PROJECT changes 'limit=20'
factory openspec list PROJECT changes/add-greeting
factory openspec read PROJECT changes/add-greeting/proposal.md
factory openspec read PROJECT changes/add-greeting/proposal.md 'offset=8192&sha256=DIGEST'
factory openspec check PROJECT add-greeting
```

MCP: factory_openspec_init/list/read/check. Project/workspace/actor come from the run, never model
arguments. Both captured and current tool filters/policies apply; denies prevail, and approval rules
require an exact action grant for init. Read tools requiring approval remain fail-closed, as for
other Factory reads. Run tokens cannot use owner endpoints. Tool audit contains tool identity,
request key and result status, not document content.

Lists scan one directory, sort names and return items/next_after/has_more. Maximum 2000 entries per
directory; limit defaults to 20 for HTTP and 10 for MCP (maximum 100/20). There is no recursive bulk
load. Reads accept a <=1 MiB regular UTF-8 document, return path/sha256/content/bytes/offset/next_offset/
has_more, and preserve UTF-8 byte boundaries. Default page 8192 bytes; HTTP max 32768, MCP max 8192.
Continuation requires the prior sha256. A changed digest returns 409; restart from offset zero to
read the new version. Listings reflect current files and are not transaction snapshots. Docker Desktop host-file propagation
can be briefly delayed: a caller coordinating an external edit should wait until the returned digest
matches the expected host content. Factory adds no document cache.

Worker omits document bodies from OpenSpec MCP read result telemetry before publishing it, preserving
path/digest/page metadata; Core also applies that filter before storing runtime events. Codex itself
still receives original content and manages its own native thread history. This is not general DLP:
arbitrary trusted shell/model output and manually pasted text may contain document excerpts.
No specification is automatically copied into Factory RunSpecs, reports or decision records.

## Verification

`make check`, `make build` and dedicated `NATS_TEST_URL=... go test -race ./internal/worker -run JetStream`
cover bounded/current reads, stale digests, UTF-8, symlink/traversal confinement, workspace mapping,
non-destructive init, writer/UNKNOWN boundaries, scoped policies, task links, queue rechecks, lack of
document copies in domain records, and native preflight before runtime startup.

`scripts/openspec-smoke.py --restart-core` is opt-in and consumes ChatGPT subscription usage. It runs
Manager -> linked Developer -> Review, uses the official OpenSpec authoring/validation CLI, checks
Docker/native files and restart, reads current edits, rejects stale digests, verifies explicit task
acceptance and checks omitted document bodies in runtime telemetry. The script records a local checkpoint before restart/edit and supports `--resume PROJECT` after
inspection, so verification can continue without recreating completed tasks. Inspect durable state
rather than blindly retrying a failed or uncertain run.
