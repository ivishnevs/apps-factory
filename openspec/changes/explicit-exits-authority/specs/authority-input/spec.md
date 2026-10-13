## ADDED Requirements

### Requirement: Authority set
Authority over a task SHALL belong to the owner, the managers of its assignee up the reporting chain, and the assignee of the task that delegated it. Only directives, comments by these principals, answers, ready/run, reassignment and new approved artifact versions SHALL be recorded as authority triggers.

#### Scenario: Peer comment
- **WHEN** an agent outside the authority set comments on a task
- **THEN** the comment SHALL be stored and shown in the next brief and the event feed, and SHALL NOT add a trigger or interrupt any run

### Requirement: Authority input reaches running work
Every MCP response to a task's live run SHALL include unseen authority input with author and rank. When several directives conflict, the response SHALL mark the one with the smallest depth (owner first) as prevailing.

#### Scenario: Two managers disagree
- **WHEN** a direct manager and the owner send conflicting directives to a running task
- **THEN** the executor SHALL see both with authors, and the owner's directive SHALL be marked as prevailing

### Requirement: Authority input wakes parked work
In the same transaction as an authority trigger, every active interruptible park created before it SHALL be overridden, and the input SHALL count as progress. An `inspect` park SHALL require an explicit answer.

#### Scenario: Waiting task gets a directive
- **WHEN** a manager directs a task parked on a wait
- **THEN** the wait SHALL end as interrupted and the task SHALL become runnable immediately without backoff

### Requirement: Urgent stop and resume
An urgent directive or urgent artifact approval SHALL revoke the live run, deliver a stop command on the per-node control subject of the run's node (never on the shared work-queue command stream), and rely on the heartbeat response as the guarantee within one heartbeat interval. The next run SHALL resume the same session with the directive first in its brief, and the revoked run SHALL NOT count as an attempt.

#### Scenario: NATS stop lost
- **WHEN** the stop command is not delivered over NATS
- **THEN** the worker SHALL stop the run after its next heartbeat response and the run outcome SHALL be `revoked`

#### Scenario: Stop while commands are queued
- **WHEN** an urgent directive is sent while run commands are waiting in the command stream
- **THEN** the stop SHALL reach the run's node without waiting behind those commands

### Requirement: Agents act only through MCP
The agent's environment SHALL NOT contain owner or user tokens or the owner CLI configuration, and the owner CLI SHALL be denied on the agent's PATH.

#### Scenario: Agent calls the owner CLI
- **WHEN** an agent runs `factory tasks list` in its shell
- **THEN** the command SHALL be refused with an instruction to use the MCP tools
