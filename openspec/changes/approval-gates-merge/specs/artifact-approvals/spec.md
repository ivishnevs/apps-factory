## ADDED Requirements

### Requirement: Approval holds the producing task
While an artifact version produced by a task has an approval waiting for people, Core SHALL refuse the task's `done`
exit and SHALL refuse merging its pull request, and the task SHALL wait as `awaiting_approval`. Approving or choosing
SHALL wake the task to merge and finish; asking for changes SHALL return it to its agent on the same branch; rejecting
SHALL end it with its pull request closed unmerged.

#### Scenario: Merge before the decision
- **WHEN** the reviewer approved the pull request and the agent runs `gh pr merge` while the owner has not decided the prototype
- **THEN** Core SHALL refuse the command and the task SHALL wait as `awaiting_approval`

#### Scenario: Owner rejects
- **WHEN** the owner rejects the prototype
- **THEN** the pull request SHALL be closed unmerged and nothing SHALL reach the default branch

### Requirement: Interactive artifacts run isolated
HTML artifacts SHALL render with scripts allowed in an opaque origin, in previews and when opened directly, and SHALL
NOT be able to read the person's session, cookies or the Factory API.

#### Scenario: Prototype preview
- **WHEN** a person opens an HTML prototype artifact in the web
- **THEN** its scripts SHALL run and the page SHALL have no access to same-origin state

### Requirement: Binding reviewer, manager by default
A task's reviewer SHALL default to the assignee's review-policy reviewer, else the team's default reviewer, else its
manager, else the team's lead, else the owner. A submit SHALL go to the task's reviewer; a submit naming another reviewer SHALL be refused. Changing the
reviewer SHALL be an authority action.

#### Scenario: Team QA reviews by default
- **WHEN** the team's default reviewer is a QA agent and the assignee's own policy names none
- **THEN** the task's reviewer SHALL be the QA agent

#### Scenario: Implementer names its own reviewer
- **WHEN** the task's reviewer is the assignee's manager and the assignee submits naming another agent
- **THEN** Core SHALL refuse and name the task's reviewer

### Requirement: Approval covers the submission
An `approved` verdict SHALL mark every artifact of the submission as checked or not checked with a note. Core SHALL
refuse `approved` when an artifact is missing or not checked.

#### Scenario: Prototype not opened
- **WHEN** a reviewer approves and marks the prototype "not checked: did not run it"
- **THEN** Core SHALL refuse and hint to check it, ask for changes or hand the review on
