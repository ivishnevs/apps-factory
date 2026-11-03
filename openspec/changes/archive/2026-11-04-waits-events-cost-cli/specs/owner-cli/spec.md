## ADDED Requirements

### Requirement: Owner console
The owner CLI SHALL provide `watch`, `status`, `log`, `show`, `answer`, `comment`, task actions (run, pause, resume, retry, reassign, cancel, reopen, reset-workspace), pauses by project, agent, quota key and node, `stop`/`start`, `costs` and `pending` over the owner API, with plain text output and a `--json` option.

#### Scenario: Why is a task not running
- **WHEN** the owner runs `factory show T`
- **THEN** the output SHALL include the task's runs with outcomes and costs and its current reason and clearer

### Requirement: Kill switch
`stop` SHALL pause the company, revoke every live run and stop nodes from claiming, letting seals complete; `start` SHALL undo the company pause and SHALL NOT be confused with resuming a task.

#### Scenario: Emergency stop
- **WHEN** the owner runs `factory stop` with five live runs
- **THEN** all five SHALL end as `revoked`, no new run SHALL be claimed, and every worktree SHALL be sealed

### Requirement: Pause scopes
The owner SHALL be able to pause and resume work by project, agent, quota key and node, and the task reason SHALL name the scope.

#### Scenario: Pause one node
- **WHEN** the owner pauses node N
- **THEN** N SHALL claim no runs, its live runs SHALL continue, and other nodes SHALL keep claiming

### Requirement: Pending count
Core SHALL expose the number of questions and escalations waiting for the owner.

#### Scenario: Two pending questions
- **WHEN** two questions and one escalation wait for the owner
- **THEN** `GET /pending/count` SHALL return 3
