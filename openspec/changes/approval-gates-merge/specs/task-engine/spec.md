## ADDED Requirements

### Requirement: Awaiting approval park
A task whose review approved its work while an artifact version it produced has an open approval SHALL park as
`awaiting_approval` (reason `parked:awaiting_approval`), cleared by the approval's decider (people, or the delegate
holding it). Authority input SHALL NOT override the park. The decision SHALL move the task: approved or chosen wakes
it (or parks it on its next open approval), changes requested returns it to its implementer READY with the request
as review feedback and the pull request approval dropped, rejected abandons its branch and wakes it. An approval that
leaves pending undecided (superseded, expired) SHALL release the task with what to do.

#### Scenario: Owner approves after the review
- **WHEN** the reviewer approved and the task parked as awaiting_approval, and the owner approves the prototype
- **THEN** the park SHALL clear and the task SHALL be runnable with an `approval_decided` trigger telling it to merge
  and exit done

#### Scenario: Owner comments while it waits
- **WHEN** the owner comments on a task parked as awaiting_approval
- **THEN** the park SHALL stay until the approval is decided
