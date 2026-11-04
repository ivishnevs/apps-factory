## MODIFIED Requirements

### Requirement: Single writer and no blind retry
Core SHALL atomically queue a run, allow one writer per project, and claim a run once. Lost leases SHALL become UNKNOWN without automatic rescheduling.

#### Scenario: Duplicate delivery
- **WHEN** a run that is already claimed is offered again or claimed again by any node
- **THEN** Core SHALL refuse the claim without starting another runtime
