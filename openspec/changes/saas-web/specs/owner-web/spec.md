## MODIFIED Requirements

### Requirement: Owner web API
Core SHALL serve the web UI's API itself, in its own process, with each person's session, role and company; changes SHALL need the `X-Factory-Web: 1` header. Core SHALL act with its owner token for the web only when no sign-in is configured and `FACTORY_WEB_LOCAL=1` is set.

#### Scenario: Action without the web header
- **WHEN** a request posts an owner action without the `X-Factory-Web: 1` header
- **THEN** the API SHALL refuse it with 400 and perform nothing

#### Scenario: Member's session
- **WHEN** a signed-in member reads the board through the web API
- **THEN** Core SHALL answer with that member's company and role, as for the member's direct requests

#### Scenario: No sign-in, no local flag
- **WHEN** Core has no sign-in configured and `FACTORY_WEB_LOCAL` is not set
- **THEN** the web API SHALL answer 401 and never act with the owner token
