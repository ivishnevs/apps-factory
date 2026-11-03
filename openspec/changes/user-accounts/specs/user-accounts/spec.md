## ADDED Requirements

### Requirement: Sign-in through OIDC
Core SHALL sign users in as an OIDC relying party of one configured issuer using the authorization code flow with PKCE, and SHALL accept an ID token only after verifying its signature, issuer, audience, expiry and nonce. Core SHALL NOT keep the provider's tokens.

#### Scenario: First sign-in with Google
- **WHEN** a person signs in with Google for the first time
- **THEN** Core SHALL create a user, an identity for the connector and subject, a personal account, and a session, and SHALL redirect to company creation

#### Scenario: Forged callback
- **WHEN** a callback arrives with a `state` that does not match the browser's login cookie
- **THEN** Core SHALL refuse it and create no session

### Requirement: Identities linked by provider subject
A user SHALL be found by the identity's provider and subject. A new identity SHALL be linked to an existing user only when its email is verified and equals that user's email.

#### Scenario: Same person through GitHub
- **WHEN** a user who signed up with Google signs in with GitHub and GitHub reports the same verified email
- **THEN** the GitHub identity SHALL be linked to the same user

#### Scenario: Unverified email
- **WHEN** a new identity reports an existing user's email without verification
- **THEN** Core SHALL NOT link it to that user

### Requirement: Server-side web sessions
A web session SHALL be a random identifier in an HttpOnly, Secure cookie whose hash is stored with expiry and revocation. State-changing requests made with a session SHALL require a CSRF header.

#### Scenario: Sign-out
- **WHEN** a user signs out
- **THEN** the session SHALL be revoked and any further request with that cookie SHALL be unauthenticated

### Requirement: Several companies per user
A signed-in user SHALL be able to create companies in their account, becoming each one's `owner`, and SHALL see all companies they are a member of.

#### Scenario: Second company
- **WHEN** a user who owns company A creates company B
- **THEN** both A and B SHALL be listed for the user and B SHALL get its own empty data and catalog

### Requirement: Company context per request
Every owner API request SHALL be resolved to exactly one company: a token's own company, or for a session the company the request names if the user is a member of it. A request naming a company the user is not a member of SHALL be answered as not found.

#### Scenario: Switching companies
- **WHEN** a user selects company B in the web UI while viewing company A's tasks
- **THEN** tasks, chats, team, skills, projects, nodes and live updates SHALL show only company B's data

#### Scenario: Foreign company
- **WHEN** a user requests `/c/x/tasks` for a company `x` they are not a member of
- **THEN** Core SHALL answer not found

#### Scenario: Token with another company header
- **WHEN** a company owner token of A is sent with a header naming company B
- **THEN** Core SHALL refuse the request

### Requirement: Membership roles
An `owner` SHALL be able to change the company's catalog, skills, secrets and tokens; a `member` SHALL be able to work with tasks, chats and approvals and read the catalog.

#### Scenario: Member edits the catalog
- **WHEN** a member saves a catalog version
- **THEN** Core SHALL refuse it as forbidden
