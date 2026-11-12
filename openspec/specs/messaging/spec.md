# messaging Specification

## Purpose
Carry durable messages, conversations and questions between agents and the owner, with bound senders.

## Requirements

### Requirement: Durable messages and conversations
Core SHALL persist messages and conversations in the transaction of their cause, with mutation replay.

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

### Requirement: Message feed rows name the route
A feed row written for a message SHALL carry the summary `sender → recipient (type): body`, so feed consumers (the owner UI's flow and activity, the CLI's `watch`, `log` and `show`) know both parties without reading the message.

#### Scenario: Question to a manager
- **WHEN** developer-1 sends a question to pm
- **THEN** the message's feed row summary SHALL read `developer-1 → pm (question): …` with the body

### Requirement: Lead brainstorms with people before creating work
When an owner or admin writes to a team lead with an idea or goal rather than an explicit, fully described task,
the lead SHALL clarify, propose and agree a plan before creating tasks, SHALL decide on its own what fits the
company's direction, and SHALL post in the chat how it understood the request, the decisions it took and the tasks
it starts.

#### Scenario: Idea from the owner
- **WHEN** the owner writes "let's add a ranked mode" to the lead
- **THEN** the lead SHALL answer with questions, options and a proposed plan, and SHALL create tasks only after
  the plan is clear, posting a summary of its understanding, its own decisions and the tasks it created

#### Scenario: Explicit task
- **WHEN** the owner writes "create a task: fix the flaky integration test in phase 2, …" with a full description
- **THEN** the lead SHALL create that task without a discussion

### Requirement: Choices are sent as options
An agent's question to a person that offers choices SHALL carry them as options, each shown as a button with free text still allowed. A question whose body lists choices without options SHALL get a hint in the tool's result: Core refuses it (nothing is sent) and the hint says to ask again with the choices as options; `options: []` sends it as free text.

#### Scenario: Profiles listed in the body
- **WHEN** an agent asks "which profile: ASC Key, Cheflet, Stashy" without options
- **THEN** the tool's result SHALL hint to resend the choices as options
