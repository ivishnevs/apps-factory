## ADDED Requirements

### Requirement: Durable messages and conversations
Core SHALL persist messages and conversations with a transactional outbox and mutation replay.

#### Scenario: Repeated send
- **WHEN** an agent repeats a message send with the same request key
- **THEN** Core SHALL replay the original response without a second message

### Requirement: Credential-bound senders
Core SHALL bind the MCP sender, project and run to the credential and SHALL enforce target, project, policy and version checks.

#### Scenario: Message to a forbidden target
- **WHEN** an agent sends to a target its communication policy does not allow
- **THEN** Core SHALL refuse the message

### Requirement: Message types and question forms
Core SHALL support notification, question, answer, review_request, review_result and escalation messages, and text, single_choice, multiple_choice and confirmation question forms.

#### Scenario: Single-choice question
- **WHEN** an agent asks a single_choice question with three options
- **THEN** the recipient SHALL be able to answer with exactly one of them

### Requirement: Separate recipient runs
Core SHALL schedule separate recipient runs and SHALL never share a developer task thread with its responder.

#### Scenario: Question to another agent
- **WHEN** a developer asks another agent a question
- **THEN** the responder SHALL answer in its own run, not in the developer's thread

### Requirement: Waiting tasks release and resume
Core SHALL release a waiting task's writer only on known terminal evidence and SHALL resume in the same task thread. It SHALL never auto-resume UNKNOWN, FAILED, CANCELLED or manually blocked work on an answer.

#### Scenario: Answer to a task with an unknown run
- **WHEN** a question is answered while the asking run's outcome is UNKNOWN
- **THEN** the task SHALL NOT resume automatically

#### Scenario: Answer after a normal end
- **WHEN** the asking run ended normally and the question is answered
- **THEN** the task SHALL resume in the same thread

### Requirement: Owner inbox
Core SHALL provide owner conversations, messages and an inbox HTTP API plus a usable CLI.

#### Scenario: Owner answers from the CLI
- **WHEN** the owner lists the inbox and answers a question with the CLI
- **THEN** the answer SHALL be recorded and delivered to the asker

### Requirement: Bounded questions with deadlines
Core SHALL reject duplicate questions, bound automatic clarification chains, persist deadlines and escalate.

#### Scenario: Deadline passes
- **WHEN** a question's deadline passes without an answer
- **THEN** Core SHALL escalate it, also after a restart

### Requirement: Explicit answers
Questions SHALL be answered explicitly; final text SHALL NOT be an implicit answer or a task acceptance.

#### Scenario: Run ends with text
- **WHEN** the recipient's run ends with final text but no answer call
- **THEN** the question SHALL stay open

### Requirement: Real demonstration
The change SHALL demonstrate an actual agent answer and resume, an owner answer across restart and a persistent owner dialog.

#### Scenario: Owner answer across restart
- **WHEN** Core restarts while an owner question is open and the owner then answers
- **THEN** the asking task SHALL resume with the answer
