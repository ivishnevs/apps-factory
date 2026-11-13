## ADDED Requirements

### Requirement: Messages wake the agent they are addressed to
A message from agent S to agent R that is filed under a task R does not own SHALL wake R's open tasks parked on a message from S. A message filed under one of R's own tasks SHALL wake only that task.

#### Scenario: Review request filed under the sender's task
- **WHEN** ui-dev's task F sends ui-designer a re-review request filed under F, and ui-designer's task B is parked on the next message from ui-dev
- **THEN** B SHALL wake with the message in its brief

### Requirement: Waits that close a cycle are refused
Core SHALL refuse `factory_exit_wait` on another agent X (a message from X, an approval X decides) with reason `wait_cycle` when X is stuck waiting on the exiting agent, directly or through at most three other agents. X is stuck when it has no live or queued run in the project, conversation runs included, and its tasks that could answer are parked. The refusal SHALL name X's waiting task, what it waits for and X's last message to the exiting agent, and SHALL say how to break the cycle.

#### Scenario: Two agents wait on each other
- **WHEN** ui-designer's task B is parked on a message from ui-dev, ui-designer has no live run, and ui-dev's task F exits with a wait on a message from ui-designer
- **THEN** Core SHALL refuse the exit with `wait_cycle`, naming B and ui-designer's last message, and F SHALL stay in its run

#### Scenario: The other agent is working
- **WHEN** the same exit happens while ui-designer has a live run
- **THEN** Core SHALL accept the wait

#### Scenario: Delegate waits on the requester
- **WHEN** producer holds a delegated approval whose requester's task is parked as awaiting_approval, and producer waits on a message from the requester
- **THEN** Core SHALL refuse with `wait_cycle` and the hint to decide the approval as changes requested with what is missing

### Requirement: Waits on an agent that was not asked are refused
Core SHALL refuse `factory_exit_wait` on a message from agent X with reason `wait_unasked` when X has nothing pending from the exiting agent: the newest message between them in the project was sent by X, or there is none. An exit that sets `expects_unprompted` SHALL be accepted and the park SHALL record it.

#### Scenario: Waiting for a re-review nobody requested
- **WHEN** ui-dev pushed a new head after ui-designer's changes-requested verdict, sent nothing since, and exits waiting on ui-designer
- **THEN** Core SHALL refuse with `wait_unasked`, saying ui-designer does not know it is awaited

#### Scenario: Promised message
- **WHEN** X's last message says it will send the result later and the agent exits with `expects_unprompted: true`
- **THEN** Core SHALL accept the wait and show it as unprompted

### Requirement: Held approvals hear their delegate
A task parked as awaiting_approval SHALL run one turn on a message filed under it from the approval's delegate or from an agent the delegate messaged about the task. Done and merge SHALL stay refused until the decision.

#### Scenario: Reviewer needs a push
- **WHEN** the delegate's reviewer asks the requester to push its branch while the approval is pending
- **THEN** the requester's task SHALL run, push and wait on the approval again, and the approval SHALL stay pending

### Requirement: Cycles formed later are swept
Core SHALL periodically evaluate the cycle rule over active parks. Each task in a cycle SHALL wake once with a `wait_cycle` trigger naming the other side and its last message. A cycle of the same tasks within 24 hours SHALL escalate once to the agents' nearest common manager, else the project lead, else the owner.

#### Scenario: Both parked before the gate could see it
- **WHEN** two tasks became parked on each other's agents through exits that were each valid when made
- **THEN** the next sweep SHALL wake both with `wait_cycle`, and a repeat within 24 hours SHALL escalate instead
