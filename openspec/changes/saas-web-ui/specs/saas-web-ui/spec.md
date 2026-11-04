## ADDED Requirements

### Requirement: Web UI served by Core
Core SHALL serve the web UI as static files on its own origin, next to its web API and sign-in, with no separate UI
server; product paths SHALL load the app and Core's machine routes SHALL keep their meaning.

#### Scenario: Opening a product page
- **WHEN** a signed-in person opens `/c/acme/tasks` on Core's address
- **THEN** Core answers the app, and the app loads its data from Core's `/api` on the same origin

#### Scenario: Machine routes untouched
- **WHEN** a node requests `/install.sh` or a signed `/nodes/...` route
- **THEN** Core answers it as before, never the app

### Requirement: Landing, sign-in and onboarding
A visitor SHALL see a landing page with sign-in; after the first sign-in the person SHALL be offered a skippable
setup wizard for their company, and skipping SHALL leave a working default company with a way back to the setup.

#### Scenario: Skipping the setup
- **WHEN** a new person signs in and skips the wizard
- **THEN** they land on Home of their default company, which shows what is still missing to start work

### Requirement: Company-scoped product
Every product page SHALL belong to the company in its address; a selector SHALL switch between the person's
companies, and the person SHALL see and change only what their role allows.

#### Scenario: Switching company
- **WHEN** a person with two companies picks the other in the selector
- **THEN** the same page opens for that company and every request is scoped to it

### Requirement: Connecting a machine from the web
The web SHALL give an admin the install command for this Core with the platform detected, downloads, live status of
the company's connectors, and a page to approve a connector's sign-in request for a company they manage.

#### Scenario: Approving a connector
- **WHEN** an admin opens the address printed by `factory-node login`, signs in and approves it for their company
- **THEN** the connector is active in that company and appears connected on the Connectors page
