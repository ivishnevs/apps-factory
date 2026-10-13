# Requirements

- Core SHALL bind each approval to an exact supported mutation, agent, project and task.
- Only the owner SHALL approve/reject/revoke, using version and idempotency key.
- Deny and capability restrictions SHALL take precedence over any grant.
- Pending, rejected, revoked and expired intents SHALL fail closed with 423.
- Blocking requests SHALL release the writer through normal runtime completion and resume
  the same task/thread after decision; UNKNOWN SHALL never resume automatically.
- Ordinary inbox answers SHALL NOT resolve approvals.
- Requests, decisions, inbox answers and task state transitions SHALL persist atomically.
- Parameter changes SHALL require a new approval; exact logical retries SHALL remain idempotent.
- Artifact version approvals and external protected executors are outside this change.
