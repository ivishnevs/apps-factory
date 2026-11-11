## ADDED Requirements

### Requirement: Session affinity
The first node that claims an agent's run on a task (or a conversation) SHALL hold that (agent, task) pair: later runs
of the pair SHALL be offered to and claimable by only that node while it is active, however long it is busy. When the
node becomes inactive (offline, paused, removed, or unable to run the agent) a release timer SHALL start; it SHALL be
cleared if the node is active again before it ends. When it ends with the node still inactive, the lock SHALL be
released: any capable node MAY take the run, start a fresh session and become the holder. A run without a session to
continue SHALL have no affinity. The pin of unpushed work SHALL keep its own rules.

#### Scenario: Rework goes back to its node
- **WHEN** node A ran the developer's task, QA returned it for rework and both nodes are idle
- **THEN** only node A SHALL get and claim the rework run, and it SHALL resume the session

#### Scenario: Busy holder
- **WHEN** node A holds the pair and has no free slot for an hour while node B is idle
- **THEN** the run SHALL wait for node A and the lock SHALL NOT be released

#### Scenario: Holder back in time
- **WHEN** node A goes offline and comes back before the release timeout
- **THEN** the timer SHALL be cleared and node A SHALL keep the lock

#### Scenario: Holder gone
- **WHEN** node A stays offline beyond the release timeout
- **THEN** another capable node SHALL take the run, start a fresh session with the context from Core and hold the lock
