## ADDED Requirements

### Requirement: File-backed specification access
Factory SHALL expose project-scoped OpenSpec directory initialization, document listing and bounded UTF-8 reads without copying document bodies into SQLite.

#### Scenario: Project document read
- **WHEN** an authorized agent reads a document
- **THEN** Core SHALL restrict the read to that project's OpenSpec tree and return a content digest and bounded page

#### Scenario: Existing content
- **WHEN** initialization is retried or an existing tree is initialized
- **THEN** existing files and schemas SHALL remain unchanged

### Requirement: Checked task references
Factory SHALL validate active change references when creating, updating, readying, dispatching and claiming linked tasks.

#### Scenario: Missing queued change
- **WHEN** a linked change disappears before claim
- **THEN** Core SHALL cancel the queued run and block its task before a runtime turn starts

### Requirement: Existing OpenSpec workflow
Factory SHALL retain task/runtime state in SQLite and leave document authoring, semantic validation and archiving to the official OpenSpec workflow.

#### Scenario: Markdown completion
- **WHEN** a document checkbox is marked complete
- **THEN** task acceptance SHALL still require Factory task review
