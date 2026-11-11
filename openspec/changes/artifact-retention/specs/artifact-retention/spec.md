## ADDED Requirements

### Requirement: Project artifact retention
A project SHALL have `artifact_retention_days` (0, the default, keeps artifact versions forever). When Core's
`FACTORY_ARTIFACT_RETENTION_MAX_DAYS` is set, the effective retention SHALL be the project's setting capped by it, a
project at 0 SHALL use the ceiling, and a project value above the ceiling SHALL be refused.

#### Scenario: Cloud ceiling
- **WHEN** Core runs with a ceiling of 30 days and the owner sets a project to 90 days
- **THEN** the change SHALL be refused and the web SHALL show that at most 30 days are kept

#### Scenario: Default keeps everything
- **WHEN** neither the project nor Core sets a retention
- **THEN** no version SHALL ever be archived

### Requirement: Superseded versions are archived
Once a day Core SHALL archive the artifact versions older than the effective retention that are neither the latest
version of their artifact, nor its latest approved version, nor under an open approval request, nor a required input
of a task that is not done or cancelled. Archiving SHALL keep the version's record (author, time, digest, size, task
links, approvals, archive listing) and set when and why it was archived, and SHALL delete the stored content only
when nothing live uses the same digest. Each run that changed anything SHALL record one feed event with the counts
and the bytes freed.

#### Scenario: Review rounds
- **WHEN** a sheet has versions v1, v2 (approved) and v3, all older than the retention of 7 days
- **THEN** v1 SHALL be archived with reason "retention: 7 days" and v2 and v3 SHALL stay

#### Scenario: Shared content
- **WHEN** an archived version has the same content as another artifact's live version
- **THEN** the content SHALL stay stored

### Requirement: Archived versions are visible, not broken
Reads of an archived version's content SHALL answer 410 Gone with when and why it was archived and the artifact's
latest version. Listings, task materials and submissions SHALL still show the version marked archived; the web SHALL
show "Archived DATE" instead of a preview or download. An agent reading the version SHALL be told its content is gone
and which version is the latest; an archived version SHALL NOT be required by a new task or submitted for approval.

#### Scenario: Old task link
- **WHEN** the owner opens a finished task whose artifact version was archived
- **THEN** the task's materials SHALL list it with "Archived" and its date, and opening it SHALL explain that the
  content is no longer stored

#### Scenario: Agent fetch
- **WHEN** an agent reads an archived version
- **THEN** it SHALL get a clear answer naming the archive and the latest version
