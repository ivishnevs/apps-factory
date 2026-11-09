## ADDED Requirements

### Requirement: Inbox badge equals the Inbox
The owner's pending count SHALL be computed from the same items the Inbox lists, and the Inbox SHALL list active task
alerts and runs to inspect with the task, project, summary, since when and the clearing action.

#### Scenario: Only an alert is pending
- **WHEN** no message or approval is open and one task alert is active
- **THEN** the badge SHALL show 1 and the Inbox SHALL show that alert

### Requirement: Promised details stay open
An owner answer that defers its details SHALL keep the question open in the Inbox as "details promised" until the
owner sends the details or withdraws, and the asking task SHALL state that it waits for the owner's details.

#### Scenario: Owner will write details later
- **WHEN** the owner answers the producer's Apple team question with "I'll send details"
- **THEN** the question SHALL stay open in the Inbox and the producer's task SHALL wait for the owner without new runs
