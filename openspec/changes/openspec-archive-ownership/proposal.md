Status: superseded by `openspec-in-git` (the last task of a change archives it in its pull request; Core creates no archive task and tells the manager about a change left active). Nothing here is implemented.

## Why

Nobody owns archiving an OpenSpec change in the normal flow. The system treats the Core-created "Archive OpenSpec
change X" task (core-owned-project-data D5) as a safety net, but in practice it is the only path:

- `prompts/manager.md` mentions the official workflow for "authoring/validation/archive" without saying who
  archives or when; no executor prompt mentions archiving at all.
- `skills/openspec/SKILL.md` tells agents that Factory creates the archive task when every linked task is done,
  so an implementer has no reason to archive in its final pull request, although Core already accepts that
  (ready/dispatch/claim/preflight fall back to `changes/archive/YYYY-MM-DD-<name>/proposal.md`, and no archive
  task is created for a change that is no longer active).
- The archive task asks its assignee to "check that the implementation matches the change" before archiving.
  That repeats the review every linked task already passed and costs a full agent run per change.
- The archive task is created only when the last linked task becomes DONE and `tasks.md` has no unchecked item.
  If an item is left unchecked at that moment nothing re-evaluates later; the change stays active until the daily
  check reports `openspec.change_orphaned` after 7 days, and that event creates no task.

Archiving itself cannot simply move to "the developer after review": a change usually spans several tasks
(only Core knows when the last one closes), the implementer has no run after its task is accepted, and archiving
needs the official OpenSpec CLI, which only nodes have.

## What Changes (options, to be decided)

- A. The last task of a change archives it: the manager puts "archive the change in the final pull request" into
  that task; the skill presents the Core archive task as the fallback when that was missed.
- B. The archive task becomes mechanical: drop the "check that the implementation matches" step, only run
  `openspec archive` and exit.
- C. Re-evaluate archive readiness when `tasks.md` changes (not only when a linked task closes), or let the orphan
  check create the archive task instead of only reporting.
- D. Archive without a model: the node runs `openspec archive` itself when Core says a change is ready (new Core
  and node mechanism; probably not worth one cheap run).

A + B (+ C) is the current leaning; nothing is decided.

## Impact

`prompts/manager.md`, `skills/openspec/SKILL.md`, the archive task text in `internal/storage/openspec_archive.go`
and its tests, `docs/openspec.md` ("Archive tasks and the daily check"), and the "OpenSpec enabled per project
with archive tasks" / "Archive only finished changes" requirements of `project-data-storage` if C or D is chosen.
