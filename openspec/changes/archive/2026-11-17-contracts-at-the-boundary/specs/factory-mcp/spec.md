## ADDED Requirements

### Requirement: Closed sets are declared in the tool schemas
Every tool argument whose valid values are a closed set SHALL be declared with an `enum` built from the same constant Core validates against, the adapter SHALL check enums before the call leaves the node, and every error for a value outside a closed set SHALL list the allowed values. An unknown filter value SHALL be refused, not answered with an empty result.

#### Scenario: Unknown report kind
- **WHEN** an agent calls `factory_report_create` with `kind: audit`
- **THEN** the call SHALL fail with an error that lists every allowed report kind

#### Scenario: Schema and validator agree
- **WHEN** the tool schemas are built
- **THEN** each closed-set argument's enum SHALL equal the set its validator accepts

### Requirement: Core's own agent texts use only valid calls
Every agent-facing text Core generates (onboarding and audit briefs, review briefs, hold hints, reminders) SHALL name only tools that exist and argument values those tools accept, checked by a test.

#### Scenario: A brief names an invalid value
- **WHEN** a Core-generated brief names a report kind that Core does not accept
- **THEN** the test SHALL fail
