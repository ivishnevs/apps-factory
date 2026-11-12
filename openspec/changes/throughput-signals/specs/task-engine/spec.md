## ADDED Requirements

### Requirement: Throughput signals
Core SHALL detect, per project, queued work while node capacity is idle or unusable, with the causes taken from the
queued-run reasons and the dependency graph, and SHALL send the project's manager one notice per episode, and raise an
owner alert marked with the manager, when it lasts longer than the company's threshold (default 30 minutes), naming
each cause and the change that would release it, without changing anything itself. Core SHALL take an agent's default
work-run limit from its role in the company config, overridable per agent. A task SHALL be able to declare node
resources its runs need, enforced at claim like the agent's.

#### Scenario: Agent limit with free slots
- **WHEN** network-dev has four queued tasks, limit 1, and nodes have four free slots for 30 minutes
- **THEN** the manager SHALL get one notice naming network-dev, its queue and limit, and the free slots

#### Scenario: Session holder paused for disk
- **WHEN** seven queued runs are tied to a node paused for a critical disk for 30 minutes
- **THEN** the manager SHALL get one notice naming the node, its free disk and the release time, and the owner's Inbox
  SHALL show the alert marked "with producer"

#### Scenario: Gate with tasks behind it
- **WHEN** seven spec tasks depend on a QA verification that waits on implementation for 30 minutes
- **THEN** the notice SHALL name the verification and the seven tasks behind it

#### Scenario: Exclusive resource
- **WHEN** two integration-gate tasks declare `mac-build: 1` and the Mac offers one
- **THEN** one SHALL run and the other SHALL stay queued with reason `resource_busy` naming the first
