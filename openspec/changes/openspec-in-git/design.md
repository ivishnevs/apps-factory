## Context

See proposal. Today: `core-owned-project-data` D2–D5 and D7 (Core-owned tree, materialization, CAS write-back,
archive tasks, import); `project-creation` onboarding imports and untracks `openspec/`. Worktrees start from the
remote or default branch (`docs/architecture.md`, task worktrees), so a file on an unmerged branch is invisible
to other tasks.

## Decisions

### D1. Proposal handoff through a merged PR
The manager (or product owner) writes a change as its own task, opens a PR and merges it (`factory_exit_done`,
no review required for the product owner) before executor tasks start; executor tasks depend on that task.
Rejected: executor tasks branching from the manager's branch (needs a per-task base branch, stacked PRs);
pushing proposals straight to the default branch (no PR at all).

### D2. The last task archives; Core notices a missed archive
The manager marks the final task of a change "archive the change in your PR"; the implementer runs
`openspec archive` before its final merge. Core no longer creates an archive task on the normal path. When the
last open task of a change becomes DONE and its `done` facts (D3) report the change still `active`, Core posts a
feed event to the project's manager: `unchecked > 0` "work remains but no task does it; create tasks or close the
change", `unchecked == 0` "the change was not archived; archive it". Optionally Core then creates a mechanical
archive task (only `openspec archive`, no re-review) as the safety net.

### D3. Change references checked on the node; change state tracked through tasks
Create/update/ready/dispatch/claim validate the name only. The node's preflight checks the proposal (active or
archived) on the task's checkout before the runtime starts. A missing change does not end the run
`setup_failed` (that outcome is retried with backoff and budget): it ends with a dedicated outcome
`change_missing` that costs no attempt, backoff or budget and holds the task with a `change_missing`
escalation to the assignee's manager (retry once the proposal is merged, or cancel), the same mechanism as
`workspace_diverged`.

Core tracks changes through tasks, without a copy of the tree and without a node-reported index:
- The changes of a project are the distinct `openspec_change` values of its tasks. Every change starts with its
  proposal task (D1), so a change made through Factory is always known.
- Progress and owners are the linked tasks, their statuses and pull requests.
- A change nobody works on: no open linked task and none closed within the orphan window (today's daily
  check, rebuilt over tasks instead of `spec_documents`).
- What only git knows comes with the facts of `done` for a task linking a change: the node computes
  `facts.openspec = {change, state: active|archived|missing, unchecked}` on the checkout after the merge
  (state of `changes/<name>/` versus `changes/archive/*-<name>/`, unchecked items of its `tasks.md`). Core keeps
  the last reported state per change.
- A small `openspec_changes` table (company, project, change, last facts, orphan/archive-notice bookkeeping)
  stays for deduplication; `spec_documents` goes away.

Not seen by Core: changes authored in the repository without any task (a person in an IDE). Accepted: work goes
through the manager, and such a change is a draft until a task links it.

### D4. Owner reads specs in git
The owner reads and edits specs in the repository (GitHub, IDE). `factory openspec read/list` go away (or read
through a node later, if needed).

### D5. Review of spec edits by prompt
Product owner: may self-merge spec PRs. Implementers: spec edits ride in the code PR; requirement changes
(proposal, `specs/` deltas) are not made silently: the reviewer hands them to the product owner, or the
implementer asks first (`factory_exit_ask`). Enforcement as policy is `company-self-service`.

### D6. No OpenSpec project setting
The project setting `openspec` (`enabled`, `dir`, `archive_assignee`) goes away: OpenSpec is always the
repository's `openspec/` directory, and a project "without OpenSpec" simply has no task linking a change. The
New project form loses its OpenSpec switch.

## Risks / Trade-offs

- Proposal handoff is slower (one PR cycle) — accepted: the spec is agreed before work starts.
- More `tasks.md` conflicts between parallel tasks of one change — agents rebase before merging anyway; each task
  checks only its own items.
- Local-only repositories (no remote): specs are committed like code; no change in behaviour.
- Projects already onboarded lose the tree stored in Core; it is not exported (no migrations before release).
  Re-add specs to such a repository by hand if needed.
- Core cannot show or edit spec contents (hosted Core, client nodes). Accepted: clients read specs in their
  repository and ask the manager to explain or change them; changes reach git through the manager's tasks. A
  late-detected wrong change reference costs one dispatch (`change_missing`), which should be rare.
- Git is required for every project, so specs are never only on one node.

## Decisions confirmed

Owner 2026-11-01: D1–D5 as above. No node-reported index: change state is tracked through tasks and `done`
facts (D3). Enforced review of spec edits is `company-self-service` (review policy, path rules).
