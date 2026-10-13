## ADDED Requirements

### Requirement: Core never reads node disks
Core SHALL store OpenSpec documents, artifact contents, report attachments and large run logs in its own storage and SHALL NOT access files on nodes. Code SHALL remain in hosted git and Core SHALL hold no repository clones or forge credentials.

#### Scenario: Core on another machine
- **WHEN** Core runs on a host that shares no filesystem with any node
- **THEN** OpenSpec reads, change checks, artifact approvals and claims SHALL work unchanged

### Requirement: Materialization for runs
The claim response SHALL list the project's current OpenSpec documents and the run's pinned approved artifact inputs by digest, and the node SHALL place them in the worktree before the runtime starts.

#### Scenario: Missing content
- **WHEN** a listed blob cannot be downloaded
- **THEN** the run SHALL end as `setup_failed` before the runtime starts

### Requirement: Digest-checked write-back
Changes to project documents SHALL reach Core only through MCP with compare-and-set on content digests, including an automatic sync of the materialized OpenSpec tree before every exit. A conflict SHALL refuse the write or exit and report the conflicting paths.

#### Scenario: Two tasks edit one spec
- **WHEN** two tasks change the same document from the same base and both exit
- **THEN** the first exit SHALL store its version and the second SHALL be refused with the current digest

### Requirement: Content-bound artifact versions
Uploaded artifact versions SHALL record their content digest, and approval SHALL bind to that digest.

#### Scenario: Same version label, different content
- **WHEN** an artifact version is approved and a different file is later uploaded under a new version
- **THEN** the approval SHALL NOT apply to the new version

### Requirement: Run logs uploaded redacted
Nodes SHALL upload run logs and report attachments to Core storage through the API, and SHALL replace known credential values before upload.

#### Scenario: Token printed by a tool
- **WHEN** a command in a run prints a forge token that the node holds
- **THEN** the uploaded log SHALL contain a redaction marker instead of the token

### Requirement: OpenSpec enabled per project with archive tasks
OpenSpec SHALL be a per-project setting, enabled by default. When the last task linked to a change becomes DONE, Core SHALL create exactly one task to archive that change.

#### Scenario: Last linked task done
- **WHEN** the final task linked to change X is marked DONE
- **THEN** one archive task for X SHALL be created and a repeated evaluation SHALL NOT create another

#### Scenario: Change without a task
- **WHEN** an OpenSpec change is not archived and no task links it
- **THEN** the daily check SHALL emit one event for it to the project's manager
