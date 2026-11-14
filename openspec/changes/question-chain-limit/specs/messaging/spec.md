## MODIFIED Requirements

### Requirement: Bounded questions with deadlines
Core SHALL reject duplicate questions, bound automatic clarification chains, persist deadlines and escalate. A clarification chain is the questions between the same two agents, in either direction, about the same task (or, without a task, in the same conversation), created within the last 24 hours and after the owner's last message on that task. Questions to or from people and Factory's own questions SHALL NOT be counted. The ninth question of a chain SHALL be refused with reason `question_limit`, naming the other agent and saying what to do instead; nothing SHALL be sent and the asking run SHALL continue. A question escalated by an earlier chain limit SHALL stay answerable by its recipient.

#### Scenario: Deadline passes
- **WHEN** a question's deadline passes without an answer
- **THEN** Core SHALL escalate it, also after a restart

#### Scenario: Two agents ask each other in a loop
- **WHEN** two agents have exchanged eight questions about one task within an hour and one asks a ninth
- **THEN** Core SHALL refuse it with `question_limit` and send nothing

#### Scenario: Review rounds over days
- **WHEN** a designer asks its reviewer for a critique round several times a day over several days, each answered
- **THEN** no question SHALL be refused or escalated

#### Scenario: The owner writes on the task
- **WHEN** the owner answers or decides on the task after eight questions between two agents
- **THEN** the next question between them SHALL be accepted

#### Scenario: Questions to people
- **WHEN** an agent sends approval requests and questions to the owner about a task
- **THEN** they SHALL NOT count toward any chain
