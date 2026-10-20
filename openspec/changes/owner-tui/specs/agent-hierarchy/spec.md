## ADDED Requirements

### Requirement: Default report in health
`GET /health` SHALL name the owner's default direct report as `default_report` (`owner.default_report` of the catalog), so owner clients can address unaddressed messages the way Core routes them.

#### Scenario: Catalog with a default report
- **WHEN** the catalog sets `owner.default_report: manager` and a client reads `GET /health`
- **THEN** the answer SHALL include `default_report: manager`
