## ADDED Requirements

### Requirement: New-project errors are visible
Submitting the new-project form with invalid fields SHALL bring the first invalid field into view and focus it.

#### Scenario: Error above the fold
- **WHEN** the owner presses Create project at the bottom of the sheet and the ID field above is refused
- **THEN** the sheet SHALL scroll to the ID field and focus it, showing its error
