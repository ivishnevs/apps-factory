Done (owner 2026-11-01: decisions confirmed, start). Deployed 2026-11-01 (migration 0006); live smoke passed the same day (project openspec-smoke-19d2150298: 3 model runs and one change_missing run). No data migration or backward compatibility:
remove the old behaviour outright. Order: specs (2), then the switch (3.1–3.3, 4.1–4.4, 5.x) in one change so
no run checks a change against Core after specs moved to git, then removal (3.4–3.6, 4.5), schema (3.7), UI and
docs (7).

## 1. Decide
- [x] 1.1 Owner confirms the proposal handoff (D1: own task with a merged PR before executor tasks).
- [x] 1.2 Owner confirms who archives (D2: the last task of a change; Core notices a missed archive).
- [x] 1.3 Owner confirms change-reference checks (D3: node preflight; change state through tasks and `done` facts, no node index).
- [x] 1.4 Owner confirms that specs are read and edited in git only (D4) and the spec review rules (D5).

## 2. Specs and docs
- [x] 2.1 Delta specs for `project-data-storage` (remove Core-owned OpenSpec, materialization, write-back, archive tasks) and `project-creation` (no import/untrack), `openspec-integration` (node check, no file access tools); drop `skip_specs`; `openspec validate --strict`.
- [x] 2.2 (with 7.3, after the code) Rewrite `docs/openspec.md`; update `docs/projects.md`, `docs/architecture.md`, `docs/factory-mcp.md`.

## 3. Core
- [x] 3.1 Task `openspec_change`: name validation only at create/update/ready/dispatch/claim; remove `runChangeCheck`/`blockOpenSpecRun` (`claim.go`, `nodes.go`) and the IN_REVIEW dispatch check (`evaluate.go` `openSpecRefusal`, `tasks.ReasonOpenSpec` and its `factory openspec import` hint).
- [x] 3.2 Run brief (`storage/brief.go`): specs are in the checkout and change with the code in the PR; the last task archives.
- [x] 3.3 New run outcome `change_missing`: blocks the task with the node's reason, no attempt, backoff or budget.
- [x] 3.4 Remove OpenSpec documents from storage and API (`spec_documents`, owner `openspec/*` endpoints, MCP `factory_openspec_*`, `internal/openspec/audit.go`), keep the blob store for artifacts, attachments and logs.
- [x] 3.5 Change tracking over tasks (D2, D3): store `facts.openspec` from `done`; on the last linked task DONE with the change still active, a feed event to the manager (unchecked work / not archived), optionally a mechanical archive task; rebuild the orphan check over tasks. Remove the tree-based `archiveChangeTask`.
- [x] 3.6 Remove `ProjectOpenSpec.ArchiveAssignee`, `ProjectSetup.OpenSpecImported/Untracked`, `protocol.Manifest.OpenSpecDir`, `SetupReport.OpenSpec/Untracked`; and the whole project setting `openspec` (D6: always `openspec/`).
- [x] 3.7 Schema migration: drop `spec_documents`, reshape `openspec_changes` for D3 bookkeeping (no data migration).

## 4. Node and adapter
- [x] 4.1 Stop materializing OpenSpec and excluding `openspec/` (keep pinned artifact inputs under `.factory/`).
- [x] 4.2 Remove the OpenSpec sync at exits (`internal/factorytools/sync.go`) and `openspec_restored`/`openspec_conflict` refusals.
- [x] 4.3 Preflight checks the change on the task's checkout (active or archived proposal); a missing change ends the run `change_missing`.
- [x] 4.4 Project setup: new repositories stop ignoring `/openspec/`; existing repositories are no longer imported or untracked.
- [x] 4.5 Remove `factory openspec import` and the other `factory openspec` owner commands.
- [x] 4.6 `done` facts: `openspec {change, state, unchecked}` computed on the checkout after the merge for a task linking a change.

## 5. Prompts and skills
- [x] 5.1 `skills/openspec/SKILL.md`: specs are committed with the code on the task branch; the last task archives in its PR.
- [x] 5.2 `prompts/manager.md`: write a change as its own task and merge it before executor tasks (which depend on it); mark the final task to archive.
- [x] 5.3 Spec review rules (D5) in manager/product-owner and implementer prompts.
- [x] 5.4 `project-audit` skill: commit baseline specs to the repository (the skill is not in this repository; the audit task brief and docs say it).

## 6. Verification
- [x] 6.1 `make check` green plus NATS tests with `-p 1` on a throwaway broker; tests for preflight on a checkout (active, archived, missing → `change_missing`), setup without import/untrack, absence of OpenSpec sync, `done` facts and change notices; OpenSpec smoke rewritten (run only with the owner's consent).
- [x] 6.2 Mark `openspec-archive-ownership` as superseded by this change.

## 7. UI and docs
- [x] 7.1 Web: project form without the "kept in Core" switch, setup state without import/untrack, feed texts (`summary.ts`, `humanize.ts`, inbox cards); change grouping and progress from tasks (optional).
- [x] 7.2 Terminal UI: `projform`, `projsetup`, demo/fixture and goldens.
- [x] 7.3 Remaining docs and prompts that describe Core-owned OpenSpec (`runtime-protocol*.md`, `task-lifecycle.md`, `skills.md`, `claude-runtime.md`, verification docs, `prompts/calorie-*.md`).
