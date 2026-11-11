## ADDED Requirements

### Requirement: Task kinds work and spec
A task SHALL be `work` (the default) or `spec`. The node SHALL check the linked OpenSpec change of a `work` task
before every run as before; a `spec` task writes or amends its linked change, so its runs SHALL carry no change for
the node to check, and its brief SHALL say to write the change. An agent that sets `openspec_change` when creating a
task SHALL set `kind`, or Core SHALL refuse the call with both kinds explained; a person's task SHALL default to
`work`. `factory_task_update` SHALL change the kind (work ⇄ spec); a conversation task SHALL keep its kind.

#### Scenario: Producer writes a new change
- **WHEN** the producer creates "Spec: OpenSpec change X" with `openspec_change: X` and `kind: spec`, and X is not on
  the default branch
- **THEN** its runs SHALL start without the change check

#### Scenario: Agent links a change without a kind
- **WHEN** an agent creates a task with `openspec_change` and no `kind`
- **THEN** Core SHALL refuse it and name `spec` (writes or amends the change) and `work` (implements a merged change)

### Requirement: Change missing escalation answers spec
A `change_missing` escalation SHALL offer `spec` (make the task a spec task and run it again) besides `retry` and
`cancel`.

#### Scenario: Owner answers spec
- **WHEN** a work task ends `change_missing` and the owner answers `spec`
- **THEN** the task SHALL become a spec task and run again without the change check
