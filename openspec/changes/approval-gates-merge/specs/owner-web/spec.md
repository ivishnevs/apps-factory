## ADDED Requirements

### Requirement: Awaiting approval, actual reviewer and review checks on the task
The task page SHALL show a task parked as `awaiting_approval` as "Awaiting your approval" (or by whom), with what the
decision does; its details SHALL show the reviewer who actually reviews; and "Checked in review" SHALL list each
submitted artifact as checked or not checked with the note, under the reviewer's name. The team page SHALL show and
change the team's default reviewer.

#### Scenario: Reviewed prototype waits for the owner
- **WHEN** the owner opens a task whose reviewer approved the prototype that waits for the owner's decision
- **THEN** the page SHALL say "Awaiting your approval" and list the reviewer's check of the prototype with its name
