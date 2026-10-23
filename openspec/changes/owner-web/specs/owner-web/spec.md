## ADDED Requirements

### Requirement: Owner web API
Factory SHALL serve the owner's web UI API over the owner client, holding the owner token itself and never returning it to the browser.

#### Scenario: Action without the web header
- **WHEN** a request posts an owner action without the `X-Factory-Web: 1` header
- **THEN** the API SHALL refuse it with 400 and perform nothing

### Requirement: Confirmed owner actions
The web UI SHALL ask for confirmation before every owner action.

#### Scenario: Cancel a task from a phone
- **WHEN** the owner taps cancel on a task
- **THEN** the UI SHALL show what will happen and act only after the owner confirms
