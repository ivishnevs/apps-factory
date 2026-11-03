Status: in progress (owner decision 2026-11-01: move OpenSpec back into the project's git repository; design
decisions confirmed the same day). Nothing is implemented yet.

## Why

Since `core-owned-project-data` a project's OpenSpec tree lives in Core (Postgres `spec_documents` + blob store),
is materialized into every run's worktree (git-excluded) and uploaded at exits with compare-and-set. An
independent review (2026-11-01) and the owner concluded that this costs more than it gives:

- **Spec edits bypass review.** The sync runs before every exit, `submit` included (`internal/factorytools/sync.go`),
  so delta specs and `tasks.md` edits reach the shared tree before review and before the pull request merges. A
  rejected or cancelled task leaves its spec edits in Core; the PR diff never shows specs, so code and spec are
  reviewed apart, and there is no "spec as of commit X".
- **The original reason was narrow.** Core had to stop reading node disks (bind mount). Code already lives in git
  with Core holding no clones or forge keys; the same works for specs. "Specs in git, checks on nodes" was never
  considered in that design; a git mirror stayed an open question.
- **It already lost data** (`docs/projects.md`: repositories that tracked `openspec/` lost it on the first run) and
  needed onboarding that untracks `openspec/` with a commit, which deletes the folder for everyone who pulls.
- **Most of the machinery protects against the scheme itself:** keeping unsynced edits across runs, restoring a
  whole change deleted by a branch switch, `FILE.factory-current` conflict files, untracking on onboarding. Git
  does all of that natively.
- Humans and other tools editing `openspec/` outside Factory are silently ignored (the folder is git-ignored).
- OpenSpec is designed as a folder in the repository: one directory per change, deltas merged into `specs/` by
  `archive`, so parallel changes rarely touch the same files.

## What Changes

- OpenSpec lives in the project's repository like code: authored on task branches, reviewed in the same pull
  request as the code, merged to the default branch. Core stores no OpenSpec documents.
- Nodes no longer materialize or sync OpenSpec, no longer exclude `openspec/` from git, and new repositories no
  longer ignore it; onboarding an existing repository no longer imports or untracks it.
- A task's `openspec_change` is checked where the files are: by the node's preflight on the task's checkout
  (exists today, `internal/worker/materialize.go`); Core validates only the name (or a node-reported index, see
  design).
- The manager's proposal reaches executors through git: the proposal is its own task whose PR is merged before
  executor tasks start (they depend on it).
- The last task of a change archives it in its pull request (the native OpenSpec path). Core tracks changes
  through the tasks linking them and the OpenSpec facts reported with `done` (state, unchecked items), and
  tells the manager when a change is left unarchived or with unchecked work; no Core copy of the tree and no
  node index. Supersedes the known issue `openspec-archive-ownership`.
- Review of spec edits follows the agents' prompts: a product owner may merge its own spec PR without review
  (`factory_exit_done`); an implementer's spec edits are reviewed with its code, and changes to requirements
  (proposal, `specs/` deltas) by a non-owner of the product go to the product owner. A configurable review
  policy is part of `company-self-service`.
- Blob store, artifacts, run logs and attachments stay in Core (their reasons are unchanged).
- No data migration and no backward compatibility (owner decision 2026-11-01: App Factory is not released, so
  it may change freely): old endpoints, tools, tables and the onboarding behaviour are removed outright, and
  OpenSpec trees already stored in Core are not exported.

## Out of scope

Editing specs in the web UI; a Factory-side spec viewer beyond what git hosting gives; per-path review rules
(`company-self-service`).

## Impact

Removed: OpenSpec parts of `internal/storage/openspec.go`, `internal/core/openspec.go`, `internal/factorytools/sync.go`,
OpenSpec materialization in `internal/worker/materialize.go`, `onboardOpenSpec` and the `/openspec/` ignore line in
`internal/worker/setup.go`, `internal/storage/openspec_archive.go`, MCP tools `factory_openspec_write/move/delete/sync`
(and `init`/`import`/`manifest`), `factory openspec import`. Schema: drop `spec_documents` and `openspec_changes`
(schema change only). Changed: `prompts/manager.md`, `skills/openspec/SKILL.md`, the
`project-audit` skill (baseline specs committed to the repository), docs `openspec.md`, `projects.md`,
`architecture.md`, `factory-mcp.md`, the OpenSpec smoke script.
