## ADDED Requirements

### Requirement: Throughput signals
Core SHALL take an agent's default run limit from its role in the company config, overridable per agent. Core SHALL
detect, per project, queued work that free node slots could take and the cause (agent limit, session affinity behind
a paused or busy node, no ready node, dependency gates), and SHALL send the project's manager one notice per episode
when it lasts longer than the configured threshold, naming the cause and the change that would release it, without
changing anything itself. Creating or updating a planning task that depends on a verification or implementation task
SHALL return a non-blocking `planning_gated` warning.

#### Scenario: Agent limit with free slots
- **WHEN** network-dev has four queued tasks, limit 1, and nodes have four free slots for 30 minutes
- **THEN** the manager SHALL get one notice naming network-dev, its queue and limit, and the free slots

#### Scenario: Spec gated on a verification
- **WHEN** the producer creates a spec task depending on a QA verification task
- **THEN** the task SHALL be created and the answer SHALL warn `planning_gated` naming the verification
