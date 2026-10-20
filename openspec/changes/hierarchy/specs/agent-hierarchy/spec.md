## ADDED Requirements

### Requirement: Reporting tree as data
Each agent SHALL declare `reports_to` as another agent of the same company or `owner`. The tree MAY have any depth, role names SHALL be data, and configuration with cycles or unknown targets SHALL be rejected.

#### Scenario: Cycle
- **WHEN** agent A reports to B and B reports to A
- **THEN** configuration validation SHALL fail and the catalog SHALL NOT be loaded

### Requirement: Authority over the subtree
The owner and the managers up an assignee's chain SHALL be able to direct, pause, resume, reassign within their subtree, cancel and reprioritize the assignee's tasks. Other agents SHALL NOT.

#### Scenario: Reassign outside the subtree
- **WHEN** a lead tries to reassign a report's task to an agent outside the lead's subtree
- **THEN** Core SHALL refuse with 403 and the task SHALL be unchanged

#### Scenario: Reprioritize a waiting task
- **WHEN** a manager reprioritizes a task parked on a wait
- **THEN** the priority SHALL change, an event SHALL be recorded, and the wait park SHALL stay active

### Requirement: Questions go up the chain
An `ask` SHALL be routed to the asker's manager. A principal SHALL answer only categories delegated to it by policy, and SHALL otherwise forward the question up. Locked categories (release, money, deletion, legal) SHALL go directly to the owner and SHALL NOT be delegable.

#### Scenario: Delegated technical choice
- **WHEN** a developer asks a technical question and its lead has the `technical` category delegated
- **THEN** the lead SHALL answer, the owner SHALL NOT be notified, and the developer's task SHALL wake with the answer

#### Scenario: Store submission
- **WHEN** an agent asks a question in category `release`
- **THEN** the question SHALL be routed to the owner regardless of delegation rules

### Requirement: Decision answers
A decision SHALL accept a free-text answer in addition to its options. The first answer SHALL win and later answers SHALL be refused with the recorded answer. A question whose task changed since it was asked SHALL be shown as possibly outdated, and open questions of a closed task SHALL be cancelled.

#### Scenario: Two principals answer
- **WHEN** a lead and the owner answer the same question one after the other
- **THEN** only the first answer SHALL be applied and the second SHALL receive 409 with it

### Requirement: Silent manager escalation
A question or escalation routed to a manager and unanswered for the configured interval SHALL move one level up automatically, with a note to the skipped manager.

#### Scenario: Lead does not answer
- **WHEN** a question to a lead remains unanswered for four hours
- **THEN** it SHALL be routed to the lead's manager and the lead's conversation SHALL record the forward

### Requirement: Persistent conversation tasks
Each agent SHALL have one long-lived conversation task with its boss, which resumes the same runtime session. Messages, questions and escalations addressed to an agent SHALL be triggers on its conversation task, and `done` on it SHALL mark input handled without closing it.

#### Scenario: Owner talks to the top manager
- **WHEN** the owner sends two messages at different times to the top manager
- **THEN** both SHALL be handled by runs of the same conversation task resuming one session

#### Scenario: Conversation run beside the project's writer
- **WHEN** the manager's conversation task runs while a writer run of the same project holds the shared workspace
- **THEN** the conversation run SHALL run at the same time in its own conversation directory, without taking the writer lock

#### Scenario: Conversation run in a repository project
- **WHEN** a conversation run, working in its own conversation directory, ends with `factory_exit_done` in a project with a repository
- **THEN** the exit SHALL be accepted without git facts, as the run owns no branch or pull request

#### Scenario: Owner with two direct reports
- **WHEN** the owner sends one message without an address and one starting with `@B`, and A is the default direct report
- **THEN** the first SHALL be a trigger on A's conversation task and the second on B's

### Requirement: Watchers woken only by what concerns them
Managers SHALL see subtree events in their briefs and the event feed and SHALL be woken only by questions or escalations routed to them and by closed tasks they delegated.

#### Scenario: Report's task fails once
- **WHEN** a report's task has a failed attempt that is retried automatically
- **THEN** the manager SHALL NOT get a run for it and SHALL see it in the next brief
