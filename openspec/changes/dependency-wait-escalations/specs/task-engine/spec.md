## ADDED Requirements

### Requirement: Dependency waits are not blocked by hand
A task that waits on open dependencies SHALL stay in its dependency wait (READY with reason `dependencies`). Core SHALL
refuse an agent's `block` action on such a task when no other hold applies, with a hint to keep the dependency wait
and answer a `stalled` escalation with `keep_waiting`. The owner's `block` action SHALL stay allowed.

#### Scenario: Agent holds a task that waits for planned work
- **WHEN** the producer calls `factory_task_block` on a READY task whose dependencies are open
- **THEN** Core SHALL refuse it, the task SHALL stay READY with reason `dependencies`, and the refusal SHALL say that
  the task already waits and how to answer the escalation

#### Scenario: Task starts when its dependencies are done
- **WHEN** every dependency of a task in its dependency wait is DONE
- **THEN** the task SHALL be dispatched without any owner action

#### Scenario: Holding for another reason
- **WHEN** an agent needs to hold a task for work that is not yet a task
- **THEN** it SHALL create that task and add it as a dependency instead of blocking by hand
