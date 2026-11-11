# review-policy Specification

## Purpose
Define which review a task needs before it can be done, and enforce that rule.

## Requirements

### Requirement: Review rule
A policy rule of type `review` SHALL name the roles, agents or teams it applies to, whether review is `required`, and optionally a default `reviewer` (an agent or role). A reviewer role SHALL resolve to the agent with that role in the project's team, and a reviewer agent SHALL belong to that team. Without any review rule the agent's prompt decides whether work goes to review.

#### Scenario: Required review before done
- **WHEN** a developer covered by a required review rule calls `factory_exit_done` for a repository task without an approval bound to its pull request
- **THEN** Core SHALL refuse the exit with `review_required` and a hint to submit for review

#### Scenario: Approved and merged
- **WHEN** the reviewer approved the pull request and the developer merged it
- **THEN** `factory_exit_done` SHALL be accepted

#### Scenario: Default reviewer
- **WHEN** a covered agent calls `factory_exit_submit` without a reviewer
- **THEN** the task SHALL go to the rule's reviewer resolved in the project's team

### Requirement: Human review is visible
A task that reaches review without an agent reviewer SHALL name the person it waits for and SHALL notify that
person in the Inbox and Telegram with the actions to accept it or send it back; people assigning such work SHALL
be told beforehand that it will come back to them.

#### Scenario: Lead submits a task
- **WHEN** the team lead submits a task a person created and no review rule covers the lead
- **THEN** the task SHALL show "waiting for review by <person>" and that person SHALL get an Inbox item and a
  Telegram message with Accept and Send back
