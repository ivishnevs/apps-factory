## 1. Task branch in the exit contract

- [x] 1.1 Node: `FACTORY_TASK_BRANCH` for implementer runs; adapter facts carry `task_branch`; `publish` pushes `HEAD`
      to the task branch and opens or updates the pull request from it.
- [x] 1.2 Core: refuse `submit`, code `handoff` and `done` in a repository project with `wrong_branch` when the facts'
      branch or the pull request's head branch differs from the task branch; the refusal is fixable and recorded per
      run; the hint names the fix.
- [x] 1.3 Tests: the 2026-11-15 case with fixtures (worktree on `af/<task>`, agent checks out another branch, pushes
      and submits → refused `wrong_branch`; after pushing to the task branch → accepted); a PR from another branch →
      refused; a conversation run is unaffected.

## 2. Failures go to whoever can fix them

- [x] 2.1 Worker: setup failures carry a code and a fixer (`implementer`, `node`, `owner`, `none`); not reviewable and
      restore-gone are `implementer`; existing node faults stay `node`.
- [x] 2.2 Core `recoverTask`: route by fixer. `implementer` sends the task back to its implementer as rework with the
      cause and does not count the reviewer's attempt (unreachable implementer → its manager). `owner` opens one
      owner item without retries. `none` keeps the bounded retry.
- [x] 2.3 The same failure code with the same handed-over head twice in a row is routed as deterministic.
- [x] 2.4 Escalation answers: `retries_exhausted` on a task in review offers `return`; `reassign` implemented for
      `retries_exhausted` as the run-recovery spec says.
- [x] 2.5 Tests: review run not reviewable → task back with the implementer, no owner escalation; a transient failure
      still retries; every escalation reason has an answer that changes who acts or what they act on.

## 3. Closed sets declared in the tool schemas

- [x] 3.1 Shared constants for report kind, message type, question type, decision status, artifact audience and task
      status; tool schemas declare them as enums; the adapter check enforces enums.
- [x] 3.2 Closed-set errors list the allowed values; `task_list` refuses an unknown status.
- [x] 3.3 `factory_report_create` failures are recorded as fixable refusals.
- [x] 3.4 Test: each schema enum equals its validator's set.

## 4. Core's own texts

- [x] 4.1 Audit brief: options as a decision proposal (`value` + `alternatives`), summary in the PR and a
      `stage_completed` report, the branch rule.
- [x] 4.2 Test over every Core-generated agent text: named `factory_*` tools exist and named argument values are valid.
- [x] 4.3 Default library `project-audit` skill (where shipped) corrected.

## 5. Docs

- [x] 5.1 `docs/explicit-exits-contract.md`: `task_branch` fact, branch rule, `wrong_branch`; failure routing in the
      run-recovery docs.

## Notes and assumptions

- 1.1: `FACTORY_TASK_BRANCH` is set for implementer runs in a task worktree with a task branch; review copies,
  conversations, `fresh_from_default` worktrees and the project workspace get none, and Core checks the branch only
  when the facts carry it. Pull request facts gain `head_branch` (`gh pr view --json headRefName`).
- 1.2: the branch check applies to exits with code (something ahead of the default branch or a pull request), so a
  `done` after the merge from the default branch is not refused. "Recorded per run": the refusal goes to
  `run_refusals` as a fixable refusal of `factory_exit_<kind>`; it never holds an exit itself, and the accepted exit
  clears it.
- 2.1: codes `not_reviewable`, `restore_gone` (implementer), `workspace_diverged`, `local_policy`, `secret_missing`,
  `skill` (owner), node fault kinds (node), `lock` (none). Project setup and materialization failures stay
  unclassified and fall under the deterministic rule (2.3).
- 2.2: the owner item and the unreachable implementer use a new escalation reason `setup_blocked` (answers retry,
  reassign, cancel). An implementer that is the failed run's own agent counts as unreachable (it cannot fix its own
  missing saved work by running again).
- 2.3: "in a row" compares the same agent's runs of the task (a reviewer's two runs with the implementer's in between
  are in a row for the reviewer).
- 2.4: `budget_exhausted` also offers `reassign`, so every reason has an answer that changes who acts.
- 3.1: the adapter checks every tool's arguments on the node except the tools whose refusals Core records for the
  run (`factory_report_create`, the forwards and questions): a refusal on the node would never be recorded, and the
  exit would pass it. Core applies the same check (same schemas) to those. `factory_report_list` `kind` is a closed
  set too (the agents' kinds, `runtime`, or empty).
- 3.3: "or the exit says why not" is the exit argument `unresolved_reason` (submit, handoff, done, wait); it passes a
  refused report, not a refused forward or question. Any report created settles the run's refused reports, whatever
  `request_id` it carries.
- 4.2: the test reads every string literal that names a `factory_*` tool in core, storage, worker, messaging,
  factorytools and tasks, plus the shipped prompts, presets and skills, and checks tool names and closed-set values
  named in the same sentence as their tool.
- 4.3: the default library (`skills/`) ships no `project-audit` skill, so there is nothing to correct here; a
  company's or the owner's own copy is their data (`factory catalog`).
- Verified with Go tests and fakes only (no live runs).
