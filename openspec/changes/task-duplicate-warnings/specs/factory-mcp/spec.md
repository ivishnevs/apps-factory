## ADDED Requirements

### Requirement: Compact task search
`factory_task_list` SHALL answer in a compact form by default (id, title, status, assignee, `openspec_change`,
`covers`, dependencies; the description only with `full: true`) and SHALL accept the filters `open`, `assignee`,
`openspec_change`, `covers` and `query` (all words of the query in the title, case-insensitive).
`factory_task_create` and `factory_task_update` SHALL accept `covers`, `replaces` and `not_duplicate_of`, and their
answers SHALL include the duplicate `warnings` with a hint on how to resolve them.

#### Scenario: One check before a batch
- **WHEN** the producer lists open tasks of change `world-models` before creating its checks
- **THEN** the answer SHALL hold only those tasks, without descriptions

#### Scenario: Warning in the tool answer
- **WHEN** an agent creates a task that warns `same_work_item`
- **THEN** the tool answer SHALL contain the created task, the warning and the hint to cancel the new task, update
  the existing one or mark them `not_duplicate_of`
