# task-engine Specification

## Purpose
Define tasks, their scheduling, their review and how a task parks for the owner.

## Requirements

### Requirement: Tasks
Core SHALL validate lifecycle actions, same-project DAG dependencies and optimistic versions.
Dependencies SHALL only unblock scheduling after DONE, not runtime completion or submission.
Duplicate mutation IDs SHALL replay the original response without repeated effects.

#### Scenario: Dependency submitted
- **WHEN** a task's dependency is submitted to IN_REVIEW
- **THEN** the task SHALL stay unschedulable until the dependency is DONE

### Requirement: Scheduling
Dispatch SHALL atomically persist task IN_PROGRESS and a unique attempt/run in the queued state.
Scheduler SHALL use priority and availability without fixed role sequences. Restart SHALL recover
from persisted state. Unknown outcomes SHALL quarantine the workspace and block automatic retry.

#### Scenario: Unknown outcome
- **WHEN** a run's outcome is unknown
- **THEN** the workspace SHALL be quarantined and the task SHALL NOT be retried automatically

### Requirement: Review
Completed runs SHALL submit to IN_REVIEW; owner/authorized reviewer SHALL explicitly accept/reject.
Rejection SHALL preserve feedback and allow a new attempt only after an explicit ready action.

#### Scenario: Rejected task
- **WHEN** the reviewer rejects a task with feedback
- **THEN** the feedback SHALL be kept and no new attempt SHALL start before an explicit ready action

### Requirement: Task parks for the owner
Core SHALL list a task's parks at `GET /tasks/{id}/parks`, oldest first, each with its id, kind, ref, reason, who clears it (`owner`, or `agent:<name>` for a question to an agent), status, run and the times it was set and cleared: the active parks, and with `?all=1` also the cleared and overridden ones. An unknown task SHALL be 404.

#### Scenario: Blocked task
- **WHEN** the owner lists the parks of a task blocked by hand
- **THEN** Core SHALL return its active `block` park with the reason, cleared by the owner

#### Scenario: Cleared park
- **WHEN** the task was readied again
- **THEN** the default list SHALL be empty and `?all=1` SHALL return the park as cleared with its time

### Requirement: Owner comments have an outcome
Every owner comment shown to a run SHALL get an outcome recorded by that run: done, forwarded with the recipient, or declined with a reason. Every exit tool SHALL refuse while a shown owner comment has no outcome.

#### Scenario: Out-of-role comment
- **WHEN** the owner comments "upload the fix to TestFlight" on a developer's task
- **THEN** the developer's run SHALL forward it to its manager or the project lead and record the outcome "forwarded"

#### Scenario: Exit without handling the comment
- **WHEN** a developer's run that was shown an owner comment calls `factory_exit_done` without an outcome for it
- **THEN** the tool SHALL refuse and list the unhandled comment

### Requirement: Unhandled comments stay open
A run that ends without an exit SHALL leave its owner comments open for the next run, and a second such run SHALL raise an alert to the owner.

#### Scenario: Run crashes after seeing a comment
- **WHEN** two runs in a row end without an exit after being shown the same owner comment
- **THEN** the comment SHALL still be open and the owner SHALL get an alert

### Requirement: The lead sees every board comment
Core SHALL put every owner comment on a task of a project, with the assignee's outcome, into the project lead's conversation brief without waking the lead.

#### Scenario: Board's word on another agent's task
- **WHEN** the owner comments "upload to TestFlight" on developer-lite's task in tracely
- **THEN** the CEO's next tracely brief SHALL carry that comment and its outcome, and the CEO SHALL NOT be woken for it

### Requirement: The lead is woken only when needed
Core SHALL wake the project lead for an owner comment when its outcome is declined, when no outcome is "done" while the task stays active past the team's limit, or at once when the comment is in a locked category. A forward to a manager who is the lead SHALL be one message, and the lead's outcome SHALL be a linked task or a refusal with a reason.

#### Scenario: Declined comment
- **WHEN** the assignee declines an owner comment
- **THEN** the lead SHALL be woken to confirm or override it

### Requirement: No comments on closed tasks
Core SHALL refuse an owner comment on a DONE or CANCELLED task and SHALL name the options to reopen the task with the comment or to ask the project lead.

#### Scenario: Comment on a done task
- **WHEN** the owner comments on a DONE task
- **THEN** Core SHALL refuse the comment and offer reopening the task with it or asking the lead

### Requirement: Throughput signals
Core SHALL detect, per project, queued work while node capacity is idle or unusable, with the causes taken from the
queued-run reasons and the dependency graph, and SHALL send the project's manager one notice per episode, and raise an
owner alert marked with the manager, when it lasts longer than the company's threshold (default 30 minutes), naming
each cause and the change that would release it, without changing anything itself. Core SHALL take an agent's default
work-run limit from its role in the company config, overridable per agent. A task SHALL be able to declare node
resources its runs need, enforced at claim like the agent's.

#### Scenario: Agent limit with free slots
- **WHEN** network-dev has four queued tasks, limit 1, and nodes have four free slots for 30 minutes
- **THEN** the manager SHALL get one notice naming network-dev, its queue and limit, and the free slots

#### Scenario: Session holder paused for disk
- **WHEN** seven queued runs are tied to a node paused for a critical disk for 30 minutes
- **THEN** the manager SHALL get one notice naming the node, its free disk and the release time, and the owner's Inbox
  SHALL show the alert marked "with producer"

#### Scenario: Gate with tasks behind it
- **WHEN** seven spec tasks depend on a QA verification that waits on implementation for 30 minutes
- **THEN** the notice SHALL name the verification and the seven tasks behind it

#### Scenario: Exclusive resource
- **WHEN** two integration-gate tasks declare `mac-build: 1` and the Mac offers one
- **THEN** one SHALL run and the other SHALL stay queued with reason `resource_busy` naming the first
