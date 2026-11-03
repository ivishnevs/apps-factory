# openspec-integration Specification

## Purpose
Link tasks to OpenSpec changes and leave authoring and archiving to the official OpenSpec workflow.

## Requirements

### Requirement: Checked task references
Factory SHALL validate the name of a change reference when creating and updating linked tasks, and the node SHALL check that the change exists on the task's checkout before every run of a linked task.

#### Scenario: Missing queued change
- **WHEN** the change linked by a task is not on the checkout of its run
- **THEN** the run SHALL end `change_missing` before a runtime turn starts and its task SHALL be held by a `change_missing` escalation

#### Scenario: Archived linked change
- **WHEN** a run of a linked task archives its change to `changes/archive/YYYY-MM-DD-<name>/`
- **THEN** the node preflight of later runs of that task (review runs included) SHALL accept the archived proposal

### Requirement: Existing OpenSpec workflow
Factory SHALL keep task and runtime state in Core and leave document authoring, semantic validation and archiving to the official OpenSpec workflow in the repository.

#### Scenario: Markdown completion
- **WHEN** a document checkbox is marked complete
- **THEN** task acceptance SHALL still require Factory task review
