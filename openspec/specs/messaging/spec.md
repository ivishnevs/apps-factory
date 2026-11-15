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

### Requirement: Visual attachments on messages and decisions
Core SHALL let an agent attach files of its worktree and references to existing materials to a message or a decision
proposal, and SHALL keep them on forward. The inbox, the chat and the decision card SHALL show attached images inline
with their captions, several images side by side, without the owner leaving Factory. A message to the owner that names
image or video files without attachments SHALL be accepted with a `visual_without_attachment` warning.

#### Scenario: Visual verdict in the inbox
- **WHEN** the producer asks the owner for a verdict and attaches three screenshots captioned classic, v1 and v2
- **THEN** the inbox card SHALL show the three images side by side with their captions next to the answer controls

#### Scenario: Path without attachment
- **WHEN** an agent asks the owner about "docs/qa/towers/side_by_side_v2.jpg" without attachments
- **THEN** the question SHALL be sent and the tool answer SHALL warn `visual_without_attachment`

### Requirement: Choices are sent as options
An agent's question to a person that offers choices SHALL carry them as options, each shown as a button with free text still allowed. A question whose body lists choices without options SHALL get a hint in the tool's result: Core refuses it (nothing is sent) and the hint says to ask again with the choices as options; `options: []` sends it as free text.

#### Scenario: Profiles listed in the body
- **WHEN** an agent asks "which profile: ASC Key, Cheflet, Stashy" without options
- **THEN** the tool's result SHALL hint to resend the choices as options

### Requirement: Pending questions keep their recipient awake
A question addressed to an agent that is still pending (not answered, forwarded, withdrawn or expired) SHALL keep a trigger on the recipient's task or conversation. A run that ends without acting on the question SHALL NOT mark it handled, and the recipient SHALL run again under the usual retry backoff and question-chain limits, with the question and the last refusal of an attempt to act on it in its brief.

#### Scenario: Forward refused, run ended
- **WHEN** an agent's forward of a question is refused for a missing brief and its run ends anyway
- **THEN** the question SHALL stay pending with that agent, the agent SHALL run again, and its brief SHALL name the question and the `brief_required` refusal

#### Scenario: Question forwarded
- **WHEN** the agent forwards the question successfully and its run ends
- **THEN** the question SHALL leave the agent's triggers and no extra run SHALL be scheduled for it

### Requirement: People attach images to messages
A person who may write in a conversation (the owner or a member) SHALL be able to attach images to a message they
send there, from the web (button, paste, drag-and-drop), the CLI and Telegram replies. Files SHALL be uploaded to Core
first and named by the message; Core SHALL refuse an upload of another company, a type that is not an image, more
than 10 files or a file over 20 MB. A person's attachments SHALL be stored and shown like an agent's attachments.

#### Scenario: Screenshot to the CEO
- **WHEN** the owner pastes a screenshot into the chat with the CEO and sends "this is how the competitor does onboarding"
- **THEN** the message SHALL show the image inline in the chat, and the CEO's next run SHALL have the image in its worktree and its path in the brief next to the message

#### Scenario: Photo from Telegram
- **WHEN** the owner replies to the producer's question in Telegram with a photo captioned "like this"
- **THEN** the answer SHALL be "like this" with the photo attached

#### Scenario: Not an image
- **WHEN** a person tries to attach a ZIP archive
- **THEN** Core SHALL refuse the upload and say which types are accepted

### Requirement: Agents see what people attach
Before every run the node SHALL download from Core the attachments of the run's task (its comments and the messages
filed under it) or, for a conversation run, of the latest 20 people's messages to its agent in the project, into the
working directory under
`.factory/inputs/` before the runtime starts, and the brief SHALL list each file's path and caption next to its
message or comment. Core SHALL keep the originals; this SHALL hold for task runs and conversation runs, with or
without a repository, on whichever node the run lands.

#### Scenario: Conversation run
- **WHEN** the lead's conversation wakes on a person's message with two images
- **THEN** both files SHALL be in the run's working directory and the brief SHALL name their paths in the order sent

#### Scenario: Run moves to another node
- **WHEN** a run of a task with an attached screenshot dies and the retry is claimed by another node
- **THEN** the retry SHALL have the screenshot in its working directory, downloaded from Core
