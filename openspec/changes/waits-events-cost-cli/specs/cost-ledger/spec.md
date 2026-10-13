## ADDED Requirements

### Requirement: Cost row for every run
Core SHALL record exactly one cost row per ended run with provider, model and token counts, and a cost that is either reported by the runtime, computed from the company's price table, or absent, with its source recorded. Stopped, failed, timed-out and lost runs SHALL be included.

#### Scenario: Revoked run
- **WHEN** a run is revoked by an urgent directive after consuming tokens
- **THEN** its cost row SHALL contain the tokens last reported for it

### Requirement: Cost queries
The owner SHALL be able to query costs by task, agent, day and model, and the total cost of a closed task.

#### Scenario: Cost of a closed task
- **WHEN** the owner asks for the cost of a DONE task with five runs
- **THEN** the result SHALL be the sum of the five rows with their sources
