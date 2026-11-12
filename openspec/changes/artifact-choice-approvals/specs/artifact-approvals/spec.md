## ADDED Requirements

### Requirement: Choice approvals
An agent SHALL be able to request one approval over 2–6 options, each an artifact version registered in the project
with a label and an optional note, and MAY name its recommendation. Core SHALL refuse a request with an unknown or
duplicate option. A request with one option SHALL be the existing yes/no approval.

#### Scenario: Three design variations
- **WHEN** product-designer requests approval with options V1, V2 and V3, each a registered prototype version, and recommends V1
- **THEN** one approval SHALL be created showing the three options, their previews and the recommendation

#### Scenario: Unknown option
- **WHEN** an option names an artifact version that is not registered in the project
- **THEN** Core SHALL refuse the request and say which option is unknown

### Requirement: Decisions on a choice
A choice approval SHALL be decided by choosing one option (with an optional comment), by asking for changes (with
required text), or by rejecting all (with a required reason). Choosing SHALL record the chosen version as approved and
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

## MODIFIED Requirements

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
