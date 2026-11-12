## MODIFIED Requirements

### Requirement: Approval decisions wake the requesting task
When an approval (action, command or artifact) that a task requested is approved or rejected, Core SHALL add the authority trigger `approval_decided` to that task naming the approval, the decision, who decided and its reason. It SHALL override the task's wait and ask parks only; blocks, children parks, escalations and a retry backoff SHALL stay. Revocation and expiry SHALL add no trigger. Other tasks that require or pin the artifact SHALL get `artifact_approved` as before. The trigger SHALL be the decider's: the owner's decision SHALL be the owner's input (author `owner`, rank 0); the decision of the agent the approval was delegated to SHALL carry that agent as author with its own rank, and its text SHALL name the agent and the delegation setting, never "the owner". The answer Core records on the approval's question SHALL be sent by the decider.

#### Scenario: Parked requester is approved
- **WHEN** a task that requested an artifact approval parked with `factory_exit_wait` and the owner approves the version
- **THEN** the task SHALL get `approval_decided`, its wait SHALL be overridden and its next brief SHALL show the approval and its reason

#### Scenario: Requester is rejected
- **WHEN** the owner rejects the approval with a reason
- **THEN** the task SHALL wake the same way and its brief SHALL show the rejection with the reason

#### Scenario: Lost run waits for its backoff
- **WHEN** the owner decides an approval of a task whose lost run's retry waits for its backoff
- **THEN** the backoff SHALL stay

#### Scenario: Delegate decides
- **WHEN** a designer's internal artifact approval is delegated to the team lead and the lead approves it
- **THEN** the designer's task SHALL wake with `approval_decided` whose author is the lead with the lead's rank and whose text says "approved by <lead> (delegated: <setting>)", and the answer on the approval's question SHALL be the lead's
