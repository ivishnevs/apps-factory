## ADDED Requirements

### Requirement: Waits on the owner go through an item
Core SHALL accept a wait on a message from the owner only when the owner has an open item for the task: an open decision of the project proposed from the task, its parent or its origin task; an approval holding the task; or an open question to the owner about the task. With none, Core SHALL refuse the wait with `wait_unanswerable` and a hint to ask or propose a decision. A message from the owner to the agent SHALL still wake the task.

#### Scenario: Nothing to answer
- **WHEN** an agent exits `factory_exit_wait` with `from: owner` and the owner has no open decision, question or approval for its task
- **THEN** Core SHALL refuse the exit with `wait_unanswerable` and the task SHALL keep running

### Requirement: Core links the owner's open item
When the owner has open decisions for the task, a wait on the owner SHALL park the task on those decisions, the exit result SHALL name them and the board SHALL show the task as waiting for the owner's decision. With an approval holding the task, the task SHALL park as awaiting that approval. With only an open question, the task SHALL keep its park on the owner's message, which the answer satisfies.

#### Scenario: Lead's decision for a delegated task
- **WHEN** a lead proposes a decision from its conversation, and a task whose origin is that conversation exits `factory_exit_wait` with `from: owner`
- **THEN** the task SHALL be parked on the decision and shown as waiting for the owner's decision

### Requirement: A recorded decision wakes tasks waiting on the owner for it
When a decision is recorded or withdrawn, Core SHALL wake, besides the tasks parked on it, the open tasks of the project from its task (that task, or tasks whose parent or origin it is) that are parked on a message from the owner or on another decision of that task, with the outcome as a trigger. Tasks not waiting and closed tasks SHALL NOT be changed.

#### Scenario: Decided in the Inbox
- **WHEN** the owner approves a lead's decision while a task delegated from the lead's conversation waits on a message from the owner
- **THEN** that task SHALL wake with the decision's outcome in its brief
