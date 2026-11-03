## ADDED Requirements

### Requirement: Cost by project
`GET /costs?by=project` SHALL group the cost ledger by the runs' project like the other groupings, with the same filters; an unknown grouping SHALL be refused with 400.

#### Scenario: Costs screen
- **WHEN** the owner UI reads the ledger since a day by project
- **THEN** Core SHALL return one group per project with its runs, tokens, cost and sources
