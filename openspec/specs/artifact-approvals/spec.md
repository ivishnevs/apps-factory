# artifact-approvals Specification

## Purpose
Keep immutable artifact versions and let the owner approve exact versions that tasks may require.

## Requirements

### Requirement: Immutable artifact versions
Artifact id, version and URI SHALL be immutable within a project and SHALL attach to producer provenance.

#### Scenario: URI mismatch
- **WHEN** a task references a registered artifact version with a different URI
- **THEN** Core SHALL fail closed

### Requirement: Version-bound owner approval
Owner approval SHALL bind the exact artifact version and SHALL remain separate from task review. Approval of v1 SHALL NOT satisfy a v2 requirement. Artifact decisions SHALL NOT authorize Factory action calls.

#### Scenario: New version after approval
- **WHEN** v1 is approved and a task requires v2
- **THEN** the task SHALL wait for approval of v2

#### Scenario: Task accepted in review
- **WHEN** a task producing an artifact is accepted in review
- **THEN** the artifact version SHALL remain unapproved

### Requirement: Tasks requiring approved versions
Tasks MAY require approved versions before execution; arbitrary roles and ordering SHALL work. Dispatch and claim SHALL validate pinned artifact approvals, and revoke SHALL stop unclaimed work. Core SHALL retain immutable approval inputs for already started runs; revoke SHALL NOT undo work.

#### Scenario: Revoke before claim
- **WHEN** the owner revokes an approval that a dispatched but unclaimed run pinned
- **THEN** the claim SHALL be refused and the task SHALL wait again

#### Scenario: Revoke after start
- **WHEN** the owner revokes an approval after a run started with it
- **THEN** the run SHALL keep its approval inputs and its work SHALL NOT be undone

### Requirement: Durable inbox and waiters
Core SHALL persist inbox entries, decisions and eligible waiters atomically and SHALL survive restart.

#### Scenario: Restart while waiting
- **WHEN** Core restarts while a task waits for an artifact approval
- **THEN** the inbox entry and the waiting task SHALL remain and an approval SHALL release the task

### Requirement: Checks on implicit registration
Deny and capability checks SHALL apply to implicit registration through task submission. Manual blocks and UNKNOWN quarantine SHALL NOT be cleared by artifact decisions.

#### Scenario: Approval of a blocked task's input
- **WHEN** the owner approves an artifact required by a manually blocked or quarantined task
- **THEN** the task SHALL stay blocked or quarantined

### Requirement: Who approves is configurable
A team or project SHALL set who approves each kind of artifact: people only (a hard gate), a named agent, or the lead
deciding from the owner's earlier answers and recorded decisions. An agent approval SHALL record the agent, the setting
and what it relied on; anything not covered SHALL go to people. In the people-only mode no agent SHALL be able to
approve.

#### Scenario: Internal art delegated
- **WHEN** internal art of the game team is set to be approved by game-designer and artist-2d submits a concept pack
- **THEN** game-designer SHALL be able to approve it without the owner, and the approval SHALL name it and the setting

#### Scenario: Hard gate on store material
- **WHEN** external artifacts are set to people only and the lead tries to approve a store screenshot
- **THEN** Core SHALL refuse and the approval SHALL wait for a person

### Requirement: No duplicate versions
Registering an artifact version whose content equals the artifact's latest version SHALL NOT create a new version;
Core SHALL answer with the existing version and say it is unchanged.

#### Scenario: Same sheet uploaded again
- **WHEN** an agent re-uploads a sheet byte-identical to the latest version
- **THEN** no new version SHALL be created and the answer SHALL name the existing version

### Requirement: Superseded approval requests close
When a newer version of an artifact is submitted for owner approval, open approval requests of its older versions
SHALL be closed as superseded, removed from the Inbox, and the waiting agent SHALL be told.

#### Scenario: v4 after v3
- **WHEN** the concepts v3 approval is open and v4 is submitted for approval
- **THEN** the v3 request SHALL be closed as superseded by v4 and only v4 SHALL be in the Inbox

### Requirement: Archive images in the Library
Images inside a zip or tar artifact SHALL be listed in the Library's Images as part of that archive version, after
checking the archive for unsafe paths and within size and count limits.

#### Scenario: Concept pack
- **WHEN** a zip with 73 PNG files is registered
- **THEN** Library → Images SHALL show those images, each marked with the archive it comes from
