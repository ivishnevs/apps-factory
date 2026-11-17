## ADDED Requirements

### Requirement: The audit ends with a decision for the owner
The onboarding audit brief SHALL ask for the next-step options as a decision proposal (the recommendation as its value, the other options as alternatives, each with a reason and a rough size), the summary in the pull request and a `stage_completed` report, and SHALL state that the work stays on the task branch it was given.

#### Scenario: Audit of an existing project
- **WHEN** Core creates the audit task of an existing project
- **THEN** its brief SHALL ask for a decision proposal with the options, not for a report with options
