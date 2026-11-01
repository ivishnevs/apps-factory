Planned: not scheduled. Confirm task 1 before starting the rest. No data migration or backward compatibility:
remove the old behaviour outright.

## 1. Decide
- [ ] 1.1 Owner confirms the proposal handoff (D1: own task with a merged PR before executor tasks).
- [ ] 1.2 Owner confirms who archives (D2: the last task of a change, no Core archive task).
- [ ] 1.3 Owner confirms change-reference checks (D3: node preflight only, or a node-reported index).
- [ ] 1.4 Owner confirms that specs are read and edited in git only (D4) and the spec review rules (D5).

## 2. Specs and docs
- [ ] 2.1 Delta specs for `project-data-storage` (remove Core-owned OpenSpec, materialization, write-back, archive tasks) and `project-creation` (no import/untrack); drop `skip_specs`; `openspec validate --strict`.
- [ ] 2.2 Rewrite `docs/openspec.md`; update `docs/projects.md`, `docs/architecture.md`, `docs/factory-mcp.md`.

## 3. Core
- [ ] 3.1 Remove OpenSpec documents from storage and API (`spec_documents`, owner `openspec/*` endpoints, MCP `factory_openspec_*`), keep blob store for artifacts and logs.
- [ ] 3.2 Task `openspec_change`: name validation only at create/update/ready/dispatch/claim (or the D3 index).
- [ ] 3.3 Remove archive task creation and the daily orphan check (`internal/storage/openspec_archive.go`), or rebuild them on the D3 index.
- [ ] 3.4 Schema migration dropping `spec_documents` and `openspec_changes` (no data migration).

## 4. Node and adapter
- [ ] 4.1 Stop materializing OpenSpec and excluding `openspec/` (keep pinned artifact inputs under `.factory/`).
- [ ] 4.2 Remove the OpenSpec sync at exits (`internal/factorytools/sync.go`) and `openspec_restored`/`openspec_conflict` refusals.
- [ ] 4.3 Preflight checks the change on the task's checkout (active or archived proposal).
- [ ] 4.4 Project setup: new repositories stop ignoring `/openspec/`; existing repositories are no longer imported or untracked.
- [ ] 4.5 Remove `factory openspec import` and the other `factory openspec` owner commands.

## 5. Prompts and skills
- [ ] 5.1 `skills/openspec/SKILL.md`: specs are committed with the code on the task branch; the last task archives in its PR.
- [ ] 5.2 `prompts/manager.md`: write a change as its own task and merge it before executor tasks (which depend on it); mark the final task to archive.
- [ ] 5.3 Spec review rules (D5) in manager/product-owner and implementer prompts.
- [ ] 5.4 `project-audit` skill: commit baseline specs to the repository.

## 6. Verification
- [ ] 6.1 `make check` green; tests for preflight on a checkout, setup without import/untrack, absence of OpenSpec sync; OpenSpec smoke updated (run only with the owner's consent).
- [ ] 6.2 Mark `openspec-archive-ownership` as superseded by this change.
