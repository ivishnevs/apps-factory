## ADDED Requirements

### Requirement: Work items and replacement links
A task SHALL carry an optional list of covered work items (`covers`): `<change>#<n.m>`, `<change>#<n>`,
`pr#<number>` or `msg:<id8>`. When `covers` is empty, Core SHALL infer keys deterministically from the title and
description and mark them inferred; when `openspec_change` is empty and exactly one known change is named, Core SHALL
infer it the same way. A create or update with `replaces` SHALL cancel each replaced open task in the same
transaction when the actor may cancel it, and SHALL record the link on both tasks.

#### Scenario: Follow-up names its item
- **WHEN** a task is created with the description "carry-over of player-profile-and-xp task 3.2" and no `covers`
- **THEN** the task SHALL get the inferred key `player-profile-and-xp#3.2`

#### Scenario: Split replaces the original
- **WHEN** the producer creates two tasks with `replaces: [P]` and may cancel P
- **THEN** P SHALL be cancelled with a reason naming the two tasks, and both tasks SHALL link to P

#### Scenario: Replacement without authority
- **WHEN** an agent creates a task with `replaces: [P]` and may not cancel P
- **THEN** the task SHALL be created, P SHALL stay open, and the answer SHALL carry an `original_open` warning

### Requirement: Duplicate warnings
After a task is created, or its title, `covers`, `openspec_change` or assignee changes, Core SHALL compare it with
the open tasks of its project (BACKLOG, READY, IN_PROGRESS, IN_REVIEW, BLOCKED) and SHALL return `warnings` with the
task, each naming the other task and its reasons: `same_work_item` (a shared covered key), `same_title` (equal
normalised titles and the same assignee), `original_open` (an open parent or replaced task) and `same_change_open`
(at most 10 open tasks of the same assignee and change, as context). Core SHALL NOT refuse or delay the mutation
because of a warning. Tasks created by the same run or with the same parent SHALL NOT be compared with each other. A
pair marked `not_duplicate_of` SHALL not warn again. An unanswered `same_work_item`, `same_title` or `original_open`
warning SHALL show on both tasks and as an attention item in the creator's manager feed until one task is closed or
the pair is marked not duplicate.

#### Scenario: Second follow-up for the same item
- **WHEN** task A covering `player-profile-and-xp#3.2` is open and a later run creates task B covering the same key
- **THEN** B SHALL be created and the answer SHALL warn `same_work_item` naming A

#### Scenario: Decomposition of many tasks
- **WHEN** one run creates eight tasks of one change for different assignees
- **THEN** none of the eight SHALL warn about the others

#### Scenario: Keep both on purpose
- **WHEN** the creator marks B `not_duplicate_of` A
- **THEN** the warning SHALL leave the board and the feed and SHALL not come back for that pair
