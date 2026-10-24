## ADDED Requirements

### Requirement: Company secrets held by Core
Core SHALL store company secrets encrypted at rest and SHALL never return a secret value through any read API, event or log.

#### Scenario: Owner sets a key
- **WHEN** the owner sets secret APPSTORETRACKER_KEY on the website
- **THEN** Core SHALL store it encrypted and list only its name and update time afterwards

### Requirement: Secrets delivered at claim
Core SHALL resolve a run's `${secret:NAME}` references at claim and deliver the values only in that claim answer, never in a stored record.

#### Scenario: Any node runs the researcher
- **WHEN** a node without a local secrets file claims a run whose MCP server needs APPSTORETRACKER_KEY held by Core
- **THEN** the run SHALL start with the key and no stored run, event or log SHALL contain its value
