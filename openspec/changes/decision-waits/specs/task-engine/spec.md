## ADDED Requirements

### Requirement: Waiting on a decision
A task SHALL be able to park on a decision (`decision_id`) without a timer. Core SHALL wake every task parked on a
decision when the decision is recorded or withdrawn, with the outcome in the task's brief, and SHALL NOT wake it
before. Such a task SHALL count as needing the decision's addressee and SHALL show the decision on the board and in the
decision's card.

#### Scenario: Owner decides
- **WHEN** VP-2 is parked on decision D and the owner approves D a day later
- **THEN** VP-2 SHALL have no runs in between and SHALL get one run with "D approved" in its brief

#### Scenario: Visible blocker
- **WHEN** VP-2 is parked on decision D addressed to the owner
- **THEN** the board SHALL show VP-2 as waiting for the owner's decision on D, the owner's "need you" count SHALL
  include it, and D's card SHALL list VP-2
