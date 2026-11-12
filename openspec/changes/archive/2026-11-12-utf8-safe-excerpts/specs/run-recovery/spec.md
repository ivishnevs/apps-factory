## ADDED Requirements

### Requirement: Stored excerpts are valid UTF-8
Core SHALL cut stored excerpts of run, event and brief text on a character boundary, so a byte limit never stores invalid UTF-8.

#### Scenario: Russian error text at the limit
- **WHEN** a run's error text in Russian is longer than the 512-byte excerpt and the limit falls inside a letter
- **THEN** Core SHALL store the excerpt without the cut letter and accept the run's events
