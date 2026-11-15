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
Tasks MAY require approved versions before execution; arbitrary roles and ordering SHALL work. Dispatch and claim SHALL
validate pinned artifact approvals, and revoke SHALL stop unclaimed work. Core SHALL retain immutable approval inputs
for already started runs; revoke SHALL NOT undo work. A version that was an option of a choice approval SHALL count as
approved only when it was chosen.

#### Scenario: Revoke before claim
- **WHEN** an approval is revoked before the task's run is claimed
- **THEN** the claim SHALL be refused and the task SHALL wait again

#### Scenario: Revoke after start
- **WHEN** an approval is revoked after the run started
- **THEN** the run SHALL keep its approval inputs and its work SHALL NOT be undone

#### Scenario: Not chosen option required
- **WHEN** a task requires V1 and the owner chose V2 in a choice approval
- **THEN** the task SHALL keep waiting for an approved V1

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

### Requirement: Choice approvals
Every artifact approval SHALL be one model: 1–6 options, each an artifact version registered in the project with a
label and an optional note, with an optional recommendation. A request for one version SHALL be an approval with one
option, not a separate kind; approving it SHALL be choosing that option. The same decisions, delegation rules, cite
checks and owner surfaces SHALL apply whatever the number of options. Core SHALL refuse a request with an unknown or
duplicate option.

#### Scenario: One version
- **WHEN** an agent requests approval of a single artifact version
- **THEN** it SHALL be an approval with one option, shown with Approve, Ask for changes and Reject, and a delegate approving it SHALL cite what people said like any choice

#### Scenario: Three design variations
- **WHEN** product-designer requests approval with options V1, V2 and V3, each a registered prototype version, and recommends V1
- **THEN** one approval SHALL be created showing the three options, their previews and the recommendation

#### Scenario: Unknown option
- **WHEN** an option names an artifact version that is not registered in the project
- **THEN** Core SHALL refuse the request and say which option is unknown

### Requirement: Decisions on a choice
An approval SHALL be decided by choosing one option (with an optional comment; for one option this is approving), by
asking for changes (with required text), or by rejecting all (with a required reason). Choosing SHALL record the chosen version as approved and
the other options as not chosen; not chosen SHALL NOT count as rejected. The requesting task SHALL receive the
decision, the chosen option or the requested changes, and who decided.

#### Scenario: Owner picks V2
- **WHEN** the owner chooses V2 with the comment "darker paywall"
- **THEN** V2's version SHALL be approved, V1 and V3 SHALL be not chosen, and the requester SHALL receive "V2 chosen by the owner: darker paywall"

#### Scenario: Owner wants changes
- **WHEN** the owner asks for changes "mix V1's icon with V3's palette"
- **THEN** no option SHALL be approved and the requester SHALL receive the requested changes

### Requirement: Delegates choose or escalate
Under a delegated mode a delegate SHALL decide a choice approval only by choosing, asking for changes, rejecting all
or escalating, and SHALL NOT approve it without choosing. Asking for changes or rejecting all SHALL need a reason and
no cite. In the people-only mode Core SHALL refuse every agent decision.

#### Scenario: Weak work sent back early
- **WHEN** a delegate rejects all options with the reason "none follows the brief"
- **THEN** the requester SHALL receive the rejection from the delegate

### Requirement: A delegate choice cites what people said
A delegate choice SHALL cite what people said that covers it: their earlier answers, recorded decisions, or company
or project rules written by people. Core SHALL refuse a cite that names none of these. A choice not covered SHALL be
escalated to people.

#### Scenario: Choice covered by project rules
- **WHEN** the project rules written by the owner say "dark theme only" and only V3 of three options is dark
- **THEN** the delegate MAY choose V3 citing those rules, and the requester SHALL receive that the delegate chose it

#### Scenario: Weak work sent back
- **WHEN** the delegate judges every option not good enough
- **THEN** it MAY reject all or ask for changes with a reason, and no person SHALL be asked

#### Scenario: Lead without a basis
- **WHEN** the CEO is the delegate and no owner answer covers which variation to take
- **THEN** the CEO SHALL escalate and the approval SHALL go to the owner with all its options

#### Scenario: Approve as material
- **WHEN** a delegate tries to approve a choice approval without choosing an option
- **THEN** Core SHALL refuse and hint to choose with cites or escalate

### Requirement: Delegates filter before escalating
A delegate escalating a choice approval MAY drop options, each with a reason, and SHALL keep at least one. People SHALL
see the kept options first and the dropped ones collapsed with the delegate's reasons, and SHALL be able to choose any
option, dropped ones included.

#### Scenario: Two of five dropped
- **WHEN** the delegate drops two of five options as off-brief and escalates
- **THEN** people SHALL see three options, and the two dropped ones collapsed with their reasons and still choosable
