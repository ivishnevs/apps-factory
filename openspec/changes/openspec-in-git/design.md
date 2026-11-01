## Context

See proposal. Today: `core-owned-project-data` D2–D5 and D7 (Core-owned tree, materialization, CAS write-back,
archive tasks, import); `project-creation` onboarding imports and untracks `openspec/`. Worktrees start from the
remote or default branch (`docs/architecture.md`, task worktrees), so a file on an unmerged branch is invisible
to other tasks.

## Decisions (proposed; confirm in task 1)

### D1. Proposal handoff through a merged PR
The manager (or product owner) writes a change as its own task, opens a PR and merges it (`factory_exit_done`,
no review required for the product owner) before executor tasks start; executor tasks depend on that task.
Rejected: executor tasks branching from the manager's branch (needs a per-task base branch, stacked PRs);
pushing proposals straight to the default branch (no PR at all).

### D2. The last task archives
The manager marks the final task of a change "archive the change in your PR"; the implementer runs
`openspec archive` before its final merge. Core no longer creates archive tasks.

### D3. Change references checked on the node
Create/update/ready/dispatch validate the name only; the node's existing preflight checks the proposal (active or
archived) on the task's checkout before the runtime starts and ends the run `setup_failed` otherwise. Option
kept open: nodes report a small index of the default branch (active change names, unchecked `tasks.md` items,
at a commit sha) after merges, if Core-side checks or the orphan report turn out to be needed.

### D4. Owner reads specs in git
The owner reads and edits specs in the repository (GitHub, IDE). `factory openspec read/list` go away (or read
through a node later, if needed).

### D5. Review of spec edits by prompt
Product owner: may self-merge spec PRs. Implementers: spec edits ride in the code PR; requirement changes
(proposal, `specs/` deltas) are not made silently: the reviewer hands them to the product owner, or the
implementer asks first (`factory_exit_ask`). Enforcement as policy is `company-self-service`.

## Risks / Trade-offs

- Proposal handoff is slower (one PR cycle) — accepted: the spec is agreed before work starts.
- More `tasks.md` conflicts between parallel tasks of one change — agents rebase before merging anyway; each task
  checks only its own items.
- Local-only repositories (no remote): specs are committed like code; no change in behaviour.
- Projects already onboarded lose the tree stored in Core; it is not exported (no migrations before release).
  Re-add specs to such a repository by hand if needed.

## Open questions

Owner to confirm D1–D5. Whether the Core-side index (D3) is needed at all.
