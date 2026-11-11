## ADDED Requirements

### Requirement: Project pause on the project
The web UI SHALL show a project's active pause where the project is shown: a Paused badge in the Projects list, and on
the project's page a banner with the reason, who paused it and when, with Resume for owners and admins; a running
project's page SHALL offer Pause to owners and admins.

#### Scenario: Paused project in the list
- **WHEN** the owner opens Projects while tracely has a project pause
- **THEN** tracely's card SHALL show Paused

#### Scenario: Resume from the project page
- **WHEN** an owner opens the paused tracely's page and confirms Resume
- **THEN** the pause SHALL be released and the banner SHALL give way to the Pause button
