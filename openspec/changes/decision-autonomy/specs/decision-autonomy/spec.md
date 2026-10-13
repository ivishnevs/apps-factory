## ADDED Requirements
### Requirement: Explicit category delegation
Core SHALL require explicit delegation in captured and current policies before atomically recording a policy-approved decision with decider and policy provenance.

#### Scenario: No delegated category
- **WHEN** an agent requests a category absent from either configuration
- **THEN** Core rejects the request without creating a decision or owner notice

### Requirement: Owner authority and consistent replacement
Core SHALL preserve owner review and rejection and SHALL require expected ID/version to replace a policy decision in the same category.

#### Scenario: Rejected owner proposal
- **WHEN** an agent tries to decide autonomously on that topic
- **THEN** Core rejects the takeover and keeps the prior approved choice

### Requirement: Durable rejection follow-up
Owner rejection SHALL create one revision message to the proposal author within the same transaction, and scheduler SHALL process it after the writer slot is available.

#### Scenario: Replayed owner rejection
- **WHEN** the identical rejection request is retried
- **THEN** the original result is returned without duplicating the revision message
