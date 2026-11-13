# run-exits Specification

## Purpose
Require every run to end through an explicit exit that is checked against its live state and unseen input.

## Requirements

### Requirement: Explicit exits
A run SHALL end its work with exactly one accepted MCP exit: handoff, submit, done, ask, block or wait. An accepted exit SHALL fix the run outcome `succeeded`, set `handled_seq` to the exit's `ack_seq`, and SHALL be returned unchanged when the same run retries it.

#### Scenario: Submit for review
- **WHEN** an executor calls `factory_exit_submit` with a summary
- **THEN** the task SHALL move to IN_REVIEW assigned to the reviewer with an `assignment` trigger, and the implementer SHALL be recorded

#### Scenario: Reviewer approves
- **WHEN** the reviewer calls `factory_exit_handoff` with verdict `approved`
- **THEN** the task SHALL return to the implementer with the approval recorded for the pull request and its approved head

### Requirement: Exit only from the current live run
Core SHALL return 410 without state changes unless the caller is the task's current live run or its ENDED, `lost`/`timed_out`, unreleased run. The late-exit check and exit SHALL be transactional. An accepted late exit SHALL mark the run `succeeded`, release it and prevent retries. Only `/agent/exit` SHALL permit that late run; all other tools and the credential endpoint SHALL refuse it.

#### Scenario: Exit after urgent stop
- **WHEN** a revoked run calls `factory_exit_done`
- **THEN** Core SHALL answer 410 and the task SHALL be unchanged

#### Scenario: Late exit of a lost run
- **WHEN** Core marked a run lost while its node was unreachable, the run is still unreleased, and its node delivers the run's handoff exit
- **THEN** Core SHALL apply the exit, the run SHALL end `succeeded` and released, and the task SHALL move on without a retry

#### Scenario: Late exit after release
- **WHEN** a lost run was already released and its task claimed again, and the old run's exit arrives
- **THEN** Core SHALL answer 410 and the task SHALL be unchanged

### Requirement: No exit past unseen input
Core SHALL refuse an exit with 409 and no state change while the task has authority input with a sequence above the exit's `ack_seq`, and SHALL refuse `done` while any trigger is above `ack_seq`. The 409 body SHALL contain the unseen input. `ack_seq` SHALL be the highest sequence the MCP adapter actually returned to the runtime.

#### Scenario: Directive during work
- **WHEN** a manager directs a running task and the executor then calls `factory_exit_done` without having seen it
- **THEN** Core SHALL answer 409 with the directive, and a repeated exit after the adapter returned it SHALL be evaluated normally

### Requirement: Conversation runs exit without git facts
A conversation run owns no branch or pull request. Its MCP adapter SHALL collect no git facts (`FACTORY_CONVERSATION=1`) and Core SHALL neither require nor read exit facts for it, whatever the project's repository.

#### Scenario: Conversation done in a repository project
- **WHEN** a conversation run of a project with `repo_url` calls `factory_exit_done` without facts
- **THEN** Core SHALL accept the exit instead of refusing `facts_required`

### Requirement: Done requires a merged pull request
When a task's exit facts show a branch with commits ahead of the default branch, `done` SHALL be accepted only if its pull request is merged, unless an authority has answered `abandon_branch`.

#### Scenario: Open pull request
- **WHEN** an agent calls `factory_exit_done` while its pull request is open
- **THEN** Core SHALL refuse with a hint to rebase, resolve conflicts, build and merge first

#### Scenario: Squash merge after approval
- **WHEN** an approved pull request is squash-merged without head changes after approval and the agent calls `factory_exit_done`
- **THEN** Core SHALL accept the exit although the merged commit does not descend from the approved head

#### Scenario: Head changed with re-review required
- **WHEN** the project policy is `review_after_conflicts: required` and the PR head changed after approval without a new approval
- **THEN** Core SHALL refuse `done` and require a new submit

### Requirement: Exit guard and no_exit
Drivers SHALL ask the runtime to call an exit before ending a turn. A turn that still ends without an exit SHALL have outcome `no_exit`, count as a failed attempt, and park the task with a `no_exit` escalation to the assignee's manager (before the hierarchy exists: an agent with role `manager`, else the owner) with answers retry, reassign, accept and cancel. `accept` SHALL take the work as is and set `handled_seq` to `wake_seq`.

#### Scenario: Agent stops talking
- **WHEN** a Codex turn completes without an exit and the follow-up guard turns also end without one
- **THEN** the run outcome SHALL be `no_exit` and the manager SHALL receive "for review — agent did not finish" with retry, reassign, accept and cancel

#### Scenario: Manager accepts
- **WHEN** the manager answers `accept` to a `no_exit` escalation
- **THEN** the park SHALL clear, the task SHALL keep its status and assignee, and it SHALL not run again until a new trigger

### Requirement: Exit delivery when Core is unreachable
The adapter SHALL persist a pending exit before POST and remove it after Core answers. On outage it SHALL tell the runtime "queued; stop". The adapter, then node, SHALL retry the same key until acceptance, final refusal or retry horizon. On stop, the node SHALL stop the runtime, deliver the exit with a separate bounded context, then send the terminal event. An undelivered exit SHALL end the run `lost`; a retried exit refused with 409 SHALL end it `no_exit`.

#### Scenario: Directive arrives while the exit is queued
- **WHEN** the adapter retries a queued exit after the process ended and Core refuses it with 409 because of a new directive
- **THEN** the run outcome SHALL be `no_exit` and the next brief SHALL contain the directive

#### Scenario: Stop arrives before the exit
- **WHEN** Core's `stop: terminal` reaches the node while the run's finished exit is still pending
- **THEN** the node SHALL deliver the exit before the terminal event, and Core SHALL decide it as a late exit

#### Scenario: Killed mid-request
- **WHEN** the run's process group is killed during `POST /agent/exit`
- **THEN** the pending exit file SHALL remain and the node SHALL deliver it

### Requirement: Exit mode in health
`GET /health` SHALL report whether explicit exits are on (`explicit_exits: on|off`) beside the served company.

#### Scenario: Smoke preflight
- **WHEN** a smoke script reads `GET /health` before any model run
- **THEN** the answer SHALL include `company` and `explicit_exits`, so the script can refuse a Core with exits off

### Requirement: Task exits for the owner
Core SHALL list a task's accepted exits at `GET /tasks/{id}/exits`, oldest first, each with its run, kind, time and the node's git and forge facts (head, branch, ahead of default, pushed, pull request number, state, head and URL), and SHALL answer 404 for an unknown task.

#### Scenario: Submitted and approved task
- **WHEN** the owner lists the exits of a task that was submitted and then approved by its reviewer
- **THEN** Core SHALL return the submit and the handoff in that order, the handoff with its pull request facts

#### Scenario: Unknown task
- **WHEN** the owner lists the exits of a task that does not exist
- **THEN** Core SHALL answer 404
