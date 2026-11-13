## ADDED Requirements

### Requirement: Done waits for the task's artifact decisions
`done` SHALL be refused with `awaiting_approval` while an artifact version the task produced has an open approval;
the hint SHALL name the approval and say to wait with `factory_exit_wait approval_id`. A wait on such an approval
SHALL park the task as `awaiting_approval` without a timer; an approval that does not hold the task SHALL be refused
with `approval_not_holding`. A rejected artifact SHALL abandon the branch, so `done` passes without a merge.

#### Scenario: Done before the decision
- **WHEN** the agent calls `factory_exit_done` with its pull request merged while the owner has not decided its prototype
- **THEN** Core SHALL refuse with `awaiting_approval` naming the approval

#### Scenario: Done after a rejection
- **WHEN** the owner rejected the prototype and the agent closes the pull request unmerged and calls `factory_exit_done`
- **THEN** Core SHALL accept the exit without a merged pull request

### Requirement: Core guards the merge command
Core's command guard SHALL check `gh pr merge` and `git push` of every run through a builtin policy: it SHALL refuse
`gh pr merge` and a push to the default branch while an artifact version the run's task produced has an open
approval or after a rejection abandoned its branch, and SHALL let every other command run.

#### Scenario: Pushing the task branch
- **WHEN** an agent whose task's prototype waits for the owner runs `git push origin af/t` and then `gh pr merge 7`
- **THEN** the push SHALL run and the merge SHALL be refused naming the approval

### Requirement: Submit goes to the task's reviewer
`factory_exit_submit` SHALL send the task to its reviewer; a task without one SHALL take the submitting agent's
default reviewer and record it. A submit naming a different reviewer SHALL be refused with
`reviewer_not_task_reviewer` naming the task's reviewer, except a reviewer a review path rule requires for files the
pull request changes.

#### Scenario: Task without a reviewer
- **WHEN** an agent with a manager and no review rule submits a task that names no reviewer
- **THEN** the task SHALL go to review by its manager, and `review_by` SHALL be the manager
