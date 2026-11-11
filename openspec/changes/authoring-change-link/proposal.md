Status: proposed 2026-11-09 by the owner ("I answered retry but the question keeps reopening"). **Decided 2026-11-11 by
the owner** (see "Decision"), replacing the link kinds and the role-based defaults below; implemented 2026-11-11.

## Decision (2026-11-11)

Seen again on 2026-11-11: the producer linked all eight P3 spec tasks to the changes they write (29 earlier spec
tasks worked only because they had no link). The owner chose explicit task kinds, simple and deterministic:

- A task is `work` (default; runs as before: the linked change is checked before every run) or `spec` (writes or
  amends its linked change; the link is not checked before a run). No role or name heuristics.
- An agent that sets `openspec_change` when creating a task must set `kind`, or Core refuses with the choice
  explained; the tool schema describes both. People's tasks default to `work`. `factory_task_update` changes it.
- The `change_missing` escalation gets the answer `spec` (make it a spec task and run again), so a wrongly typed
  task is fixed with one answer.
- Not now: spec-only file checks on submit, automatic waits of work tasks on a spec task, `unlink`.

Earlier (2026-11-09):
The prod instance was fixed by hand the same day (task `f31bb427`: `openspec_change` cleared, then retry).

## Why

A task that *writes* an OpenSpec change is linked to that change like a task that implements it. Before every run
the node checks that the linked change is on the checkout, so the authoring task can never start: its job is to create
the change. On 2026-11-09 in ilya-75ba24 two producer tasks hit this:

- `fec2e5c8` "Spec: OpenSpec change teleport-over-low-solids" and `f31bb427` "Spec: OpenSpec change
  talent-pi-thick-hide" ended `change_missing` and escalated to the owner with "retry or cancel";
- the owner answered retry twice on `f31bb427`; each retry ended `change_missing` again and opened the same question
  (attempt 4), because nothing had changed. Neither answer was right: retry loops, cancel drops a valid task.

## What Changes

- **Two kinds of change link.** A task links a change either as `implements` (today's meaning, checked before every
  run) or as `authors` (the task writes the change; the node does not require it before the run). Lead tools and the
  API take `change_link: implements|authors`; a task whose assignee is the change's author (a lead's "Spec:" task)
  defaults to `authors`. After the authoring task's pull request is merged, implementation tasks link the change as
  `implements` as today.
- **The authoring task checks its own result.** When an `authors` task submits or exits done, Core requires the change
  to be on its branch (`changes/<name>/proposal.md`), so a spec task cannot finish without writing the spec.
- **`change_missing` escalation that can resolve itself.** The escalation gets a third answer, `unlink` (clear the
  task's change link and continue), and `retry` is offered only when something changed since the last check (the
  default branch moved, or the link was edited); otherwise the escalation says why retry would fail again. A repeated
  `change_missing` on the same commit and link does not open a new question: the open one stays.

Not in scope: changing how implementation tasks are checked; OpenSpec validation.

## Open questions (with recommendations)

1. **Default for leads' "Spec:" tasks.** `authors` when the assignee is a lead and the change does not exist yet
   (recommended), or always explicit.
2. **`unlink` answer.** Offered to the owner and the assignee's manager (recommended), or owner only.

## Impact

Core: `change_link` on tasks and lead tools, done/submit check for `authors`, the escalation's answers and the no-repeat
rule. Node: skip the preflight for `authors` links. Producer prompt: spec tasks link their change as `authors`. Spec:
`openspec-integration`. Related: `project-data-storage` (change references checked on the node).
