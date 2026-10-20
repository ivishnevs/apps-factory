## ADDED Requirements

### Requirement: Cost row for every run
Core SHALL record exactly one cost row per ended run with provider, model and token counts, and a cost that is either reported by the runtime, computed from the company's price table, or absent, with its source recorded. Stopped, failed, timed-out and lost runs SHALL be included.

#### Scenario: Revoked run
- **WHEN** a run is revoked by an urgent directive after consuming tokens
- **THEN** its cost row SHALL contain the tokens last reported for it

#### Scenario: Run of a resumed session
- **WHEN** a run resumes a runtime session whose earlier runs already have cost rows, and the runtime reports usage and cost cumulative over the session
- **THEN** the run's cost row SHALL contain only the tokens and cost of that run, so the rows of the session's runs sum to the session's totals

#### Scenario: Running and final usage
- **WHEN** a run reports running usage during the turn and final usage with its result
- **THEN** its cost row SHALL contain the final usage, not the sum of both

### Requirement: Cost queries
The owner SHALL be able to query costs by task, agent, day and model, and the total cost of a closed task.

#### Scenario: Cost of a closed task
- **WHEN** the owner asks for the cost of a DONE task with five runs
- **THEN** the result SHALL be the sum of the five rows with their sources

### Requirement: Run model is the main model
The model of a run's cost row SHALL be the model that processed the most tokens in the run, counting input, output, cache read and cache write tokens.

#### Scenario: Auxiliary model with uncached calls
- **WHEN** a Claude run's main model reads most of its prompt from cache and an auxiliary model makes a few uncached calls
- **THEN** the cost row SHALL name the main model
