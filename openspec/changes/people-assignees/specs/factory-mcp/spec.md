## ADDED Requirements

### Requirement: Agents and people's tasks
`factory_task_create` and `factory_task_update` SHALL accept `assignee: member:<user>` from an agent only with a reason
citing that person's message or directive. An agent's change of the status, assignee or description of a person's
task, its cancellation or its replacement SHALL be refused with a hint to comment instead; reading, commenting and
depending on it SHALL be allowed.

#### Scenario: "I'll do it myself"
- **WHEN** the owner writes to the producer "I'll do the main menu layout myself" and the producer reassigns that task
  to `member:owner` citing the message
- **THEN** the task SHALL be the owner's and the producer's dependent tasks SHALL keep their dependencies on it

#### Scenario: Agent tries to take it back
- **WHEN** an agent updates the assignee of a person's task to itself
- **THEN** the update SHALL be refused and the task SHALL stay with the person
