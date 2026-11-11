# decision-autonomy Specification

## Purpose
Let the owner delegate decision categories explicitly while keeping owner authority and rejection follow-up.

## Requirements

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

### Requirement: Lead decides from earlier answers
Where a team or project lets the lead decide from earlier answers, the lead SHALL record a decision without a new
approval when the owner's earlier answers or recorded decisions cover it, citing them; a decision they do not cover
SHALL go to people. A people-only setting SHALL make every decision of that category wait for a person.

#### Scenario: Covered by an earlier answer
- **WHEN** the owner answered "best of 3" earlier and the lead must decide the match length of a new mode
- **THEN** the lead SHALL record best of 3 citing that answer, with no approval in the Inbox
