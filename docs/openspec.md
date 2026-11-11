# OpenSpec in the project repository

OpenSpec is the source for product requirements and change history, and it lives where the code lives: the
`openspec/` directory of the project's git repository (openspec-in-git). Specs are authored on task branches,
reviewed in the same pull request as the code and merged to the default branch, so any commit of the default
branch holds the specs its code was reviewed against. Core stores no OpenSpec documents, nodes do not
materialize or sync them, and git does not ignore them. Factory keeps task status, dependencies, attempts,
messages and review in its database, plus a task's `openspec_change` name. This follows the official
[OpenSpec concepts](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md).

There is no project setting for OpenSpec: a project uses it when its repository has `openspec/` and its tasks
link changes. New repositories (`repo_mode` local or github) ignore only `/.factory/`; an existing repository
is set up as it is ([projects](projects.md#onboarding-an-existing-repository)).

## Authoring

Agents read specs in their checkout and author them with normal file tools or the installed official OpenSpec
CLI; Core has no Node or OpenSpec subprocess dependency and no OpenSpec MCP tool. Inspect the CLI's help before
using commands; Factory does not reproduce schema instructions, semantic validation or archive behaviour:

```sh
openspec new change add-greeting --json
openspec validate add-greeting --strict --json --no-interactive
openspec archive add-greeting
```

The owner reads and edits specs in the repository (GitHub, an IDE) like any other file.

## The change workflow

1. **Proposal.** The manager (or product owner) writes the change (proposal, design, delta specs, `tasks.md`)
   as its own task, opens its pull request and merges it (`factory_exit_done`; a product owner may merge its
   own spec pull request without review). Worktrees start from the default branch, so a change on an
   unmerged branch is invisible to other tasks.
2. **Implementation.** Executor tasks link the change (`openspec_change`) and depend on the proposal task.
   Their spec edits (checkboxes of their own `tasks.md` items, specs their code made inaccurate) ride in their
   code pull request. Requirement changes (proposal, `specs/` deltas) are not made silently: the implementer
   asks first (`factory_exit_ask`) or the reviewer hands them to the product owner. Enforcing this as a
   policy is part of `company-self-service`.
3. **Archive.** The manager marks the final task of the change "archive the change in your pull request";
   its implementer runs `openspec archive NAME` before the final merge. Parallel tasks of one change may
   conflict on `tasks.md`; they rebase before merging like for any other file.

## Task links and the node check

`openspec_change` is a change name such as `add-greeting` (kebab-case), not a path, archive name or URL.
Core validates only the name (create, update). Before every run of a linked task the node checks the change
on the task's checkout: `openspec/changes/<name>/proposal.md`, or the latest archived
`openspec/changes/archive/YYYY-MM-DD-<name>/proposal.md` (a run of the task archived it; review and rework go
on), regular, non-empty UTF-8, at most 1 MiB. A missing change ends the run with outcome `change_missing`
before the runtime starts: it costs no attempt, backoff or budget, and opens a `change_missing` escalation to
the assignee's manager (answers `spec` when the task writes the change, `retry` once the proposal is merged, or
`cancel`).

**Task kinds (task-kinds).** A linked task is `work` (the default: it implements the change, checked as above) or
`spec` (it writes or amends the change: the run carries no change for the node to check, and the brief says to
write it in `openspec/changes/<name>`). An agent that sets `openspec_change` in `factory_task_create` must set
`kind`; a person's task defaults to `work`. `factory_task_update` changes `kind` (work ⇄ spec); the `spec` answer of
a `change_missing` escalation does it and runs the task again. The web shows a spec task's link as "Writes spec".

The brief names the change and where it is in the checkout; documents are not copied into RunSpecs, reports or
decision records. Changing Markdown checkboxes never changes Factory task status.

## Change state through tasks

Core knows a project's changes from the tasks that link them. What only git knows arrives with the facts of a
`done` exit of a linked task: the MCP adapter reads the change on the default branch once the work is merged
(or on HEAD otherwise) and reports `facts.openspec = {change, state: active|archived|missing, unchecked}`.
Core keeps the last report per change (`openspec_changes`).

When the last open task linking a change becomes DONE and the reported state is `active`, the manager of its
assignee gets one feed event: `openspec.change_unfinished` (the change's `tasks.md` still has unchecked items
and no task works on them: create tasks, or update `tasks.md` and archive) or `openspec.change_unarchived`
(finished but not archived). An archived change gets nothing. The daily check reminds once more
(`openspec.change_orphaned`, for the owner's default report) of a change still reported active that no open
task and no task closed within 7 days links; new facts reset the reminder.

A change authored in the repository without any task linking it is not seen by Core: it is a draft until a
task links it.

## Verification

`make check` covers the node preflight on a checkout (active, archived, missing → `change_missing`), the
`change_missing` escalation without an attempt, the `done` facts and the manager notices (not while another
task of the change is open, none for an archived change, facts naming another change ignored), the orphan
reminder once, the claim manifest without OpenSpec, setup of new and existing repositories without import or
untracking, and the retired `/openspec/` exclude dropped from existing worktrees.

`scripts/openspec-smoke.py` (opt-in, consumes subscription usage, run only with the owner's consent) runs the
workflow on a local repository under `.cache` and a node of this machine: the manager commits change
`add-greeting` and creates the linked executor depending on its task; the executor implements, checks its
items, archives the change and exits done with facts `archived`; no manager notice follows. Then a task linked
to a change that is not in the repository ends `change_missing` without starting a runtime and opens its
escalation. Results land in `.cache/openspec-verification.json`.
