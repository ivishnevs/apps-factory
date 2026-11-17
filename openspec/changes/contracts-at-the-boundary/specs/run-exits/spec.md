## ADDED Requirements

### Requirement: Handed-over work is on the task branch
A code exit (`submit`, `handoff` with code, `done`) of an implementer run in a repository project SHALL carry the task branch in its facts, and Core SHALL refuse it with the fixable refusal `wrong_branch`, whose hint names the fix, when the checked-out branch or the pull request's head branch is not the task branch. The adapter's publish SHALL push `HEAD` to the task branch whatever branch is checked out.

#### Scenario: Agent switched branches
- **WHEN** an implementer whose worktree was created on `af/<task>` checks out another branch, pushes it, opens a pull request from it and exits `submit`
- **THEN** Core SHALL refuse the exit with `wrong_branch` and the run SHALL keep running so the agent can push to the task branch and exit again

#### Scenario: Work on the task branch
- **WHEN** the facts' branch and the pull request's head branch are the task branch
- **THEN** the branch check SHALL pass and the other exit checks SHALL apply unchanged

### Requirement: Failed reports are held against exits
A failed `factory_report_create` call SHALL be recorded as a fixable refusal of the run, so the run SHALL NOT exit past it until a report is created or the exit says why not.

#### Scenario: Done after a failed report
- **WHEN** an agent's `factory_report_create` call fails and the agent then exits `done` without a successful report
- **THEN** Core SHALL refuse the exit as past an unresolved refusal

#### Scenario: Done that says why there is no report
- **WHEN** an agent's `factory_report_create` call fails and the agent exits `done` with `unresolved_reason` saying why the report is not created
- **THEN** Core SHALL accept the exit

#### Scenario: Refused branch is recorded
- **WHEN** Core refuses an exit with `wrong_branch`
- **THEN** the refusal SHALL be recorded for the run as a fixable refusal of the exit tool, and the run's accepted exit SHALL clear it
