## ADDED Requirements

### Requirement: Fixable refusals are recorded per run
Core SHALL record per run the fixable refusals (a missing brief, a too-long or invalid field) of the tools that send or forward to someone, with their request identifier, and SHALL clear one when a later call with the same request identifier succeeds. Refusals that are decisions (the question-chain limit, a closed task, missing authority) SHALL NOT be recorded.

#### Scenario: Fixed by a retry
- **WHEN** a forward refused with `brief_required` is called again with the same request identifier and a brief
- **THEN** the forward SHALL be sent and the recorded refusal cleared

### Requirement: No exit past an unresolved refusal
While a run has a recorded refusal, Core SHALL refuse its `done`, `handoff`, `submit` or `wait` exit with `unresolved_refusal`, naming the call, its refusal and how to fix it. `ask` and `block` exits SHALL be accepted.

#### Scenario: Forward and exit in one turn
- **WHEN** an agent calls `factory_question_forward` without a required brief and `factory_exit_done` in the same turn
- **THEN** Core SHALL refuse the forward with `brief_required` and the exit with `unresolved_refusal`, and the run SHALL continue

#### Scenario: Fixed and exited
- **WHEN** the agent calls the forward again with the same request identifier and a brief, then exits
- **THEN** the forward SHALL be sent and the exit accepted

### Requirement: Exits after a decision refusal go to the manager
When a run had a refusal that is a decision (the question-chain limit, a closed task, missing authority) and the agent then exits `done`, `handoff`, `submit` or `wait`, Core SHALL accept the exit, mark it on the task with the refused call, and send the agent's manager a non-blocking notice naming the task, the refused call and the exit. When the manager is a person the notice SHALL be an attention item in the feed, not an inbox question.

#### Scenario: Gave up after the question limit
- **WHEN** a developer's question to the designer is refused by the question-chain limit and the developer exits `done`
- **THEN** the exit SHALL be accepted, the task SHALL show that the question was refused and the agent exited without asking, and the developer's manager SHALL receive a notice and run on it

#### Scenario: Manager is the owner
- **WHEN** the same happens to an agent whose manager is the owner
- **THEN** the owner SHALL see an attention item in the feed and no inbox question
