## 1. Task branch in the exit contract

- [ ] 1.1 Node: `FACTORY_TASK_BRANCH` for implementer runs; adapter facts carry `task_branch`; `publish` pushes `HEAD`
      to the task branch and opens or updates the pull request from it.
- [ ] 1.2 Core: refuse `submit`, code `handoff` and `done` in a repository project with `wrong_branch` when the facts'
      branch or the pull request's head branch differs from the task branch; the refusal is fixable and recorded per
      run; the hint names the fix.
- [ ] 1.3 Tests: the 2026-11-15 case with fixtures (worktree on `af/<task>`, agent checks out another branch, pushes
      and submits → refused `wrong_branch`; after pushing to the task branch → accepted); a PR from another branch →
      refused; a conversation run is unaffected.

## 2. Failures go to whoever can fix them

- [ ] 2.1 Worker: setup failures carry a code and a fixer (`implementer`, `node`, `owner`, `none`); not reviewable and
      restore-gone are `implementer`; existing node faults stay `node`.
- [ ] 2.2 Core `recoverTask`: route by fixer. `implementer` sends the task back to its implementer as rework with the
      cause and does not count the reviewer's attempt (unreachable implementer → its manager). `owner` opens one
      owner item without retries. `none` keeps the bounded retry.
- [ ] 2.3 The same failure code with the same handed-over head twice in a row is routed as deterministic.
- [ ] 2.4 Escalation answers: `retries_exhausted` on a task in review offers `return`; `reassign` implemented for
      `retries_exhausted` as the run-recovery spec says.
- [ ] 2.5 Tests: review run not reviewable → task back with the implementer, no owner escalation; a transient failure
      still retries; every escalation reason has an answer that changes who acts or what they act on.

## 3. Closed sets declared in the tool schemas

- [ ] 3.1 Shared constants for report kind, message type, question type, decision status, artifact audience and task
      status; tool schemas declare them as enums; the adapter check enforces enums.
- [ ] 3.2 Closed-set errors list the allowed values; `task_list` refuses an unknown status.
- [ ] 3.3 `factory_report_create` failures are recorded as fixable refusals.
- [ ] 3.4 Test: each schema enum equals its validator's set.

## 4. Core's own texts

- [ ] 4.1 Audit brief: options as a decision proposal (`value` + `alternatives`), summary in the PR and a
      `stage_completed` report, the branch rule.
- [ ] 4.2 Test over every Core-generated agent text: named `factory_*` tools exist and named argument values are valid.
- [ ] 4.3 Default library `project-audit` skill (where shipped) corrected.

## 5. Docs

- [ ] 5.1 `docs/explicit-exits-contract.md`: `task_branch` fact, branch rule, `wrong_branch`; failure routing in the
      run-recovery docs.
