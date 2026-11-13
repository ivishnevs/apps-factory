## ADDED Requirements

### Requirement: Team default reviewer
A team MAY name a default reviewer (`spec.review.reviewer`: an agent of the team or `role:<role>`). It SHALL review
the work of the team's agents whose own review rule names no reviewer, before their managers, and SHALL never review
its own work. A default reviewer outside the team, or a role no agent of the team has, SHALL be refused.

#### Scenario: QA reviews the team's work
- **WHEN** the team's default reviewer is `role:qa` and a developer without a review rule reviewer gets a task
- **THEN** Core's suggested and default reviewer SHALL be the team's QA agent, and the QA agent's own tasks SHALL
  default to its manager

### Requirement: Changing a task's reviewer is an authority action
Changing a task's reviewer SHALL be allowed to people, to the task's creator and to the managers up the chain of
the agent whose work it reviews (the implementer while the task is in review), and refused to any other agent,
the assignee included.

#### Scenario: The implementer picks its reviewer
- **WHEN** the assignee calls `factory_task_update` with another `review_by` on its own task
- **THEN** Core SHALL refuse it with 403 and the reviewer SHALL stay

### Requirement: A reviewer hands the review on
A reviewer that cannot check the work SHALL be able to hand the review to another agent with
`factory_exit_handoff` without a verdict; the task SHALL stay in review with that agent as its reviewer.

#### Scenario: Prototype needs a designer's eye
- **WHEN** the reviewer of a task in review hands it to another agent without a verdict
- **THEN** the task SHALL stay IN_REVIEW, assigned to that agent as its reviewer, with the implementer unchanged

### Requirement: Approved verdict is not given over people's decision
An `approved` verdict SHALL be refused while the latest approval of a submitted artifact version asked for changes
or rejected it.

#### Scenario: People asked for changes during the review
- **WHEN** people asked for changes on the submitted prototype and the reviewer then gives `approved`
- **THEN** Core SHALL refuse with `artifact_decided_against` and a hint to give `changes_requested`
