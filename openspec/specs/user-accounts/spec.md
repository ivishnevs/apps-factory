# user-accounts Specification

## Purpose
Let people sign in on the website, get a company at sign-up and work in their companies with a role, each request
scoped to one company.

## Requirements

### Requirement: Sign-in through OIDC
Core SHALL sign users in as an OIDC relying party of one configured issuer using the authorization code flow with PKCE, and SHALL accept an ID token only after verifying its signature, issuer, audience, expiry and nonce. Core SHALL NOT keep the provider's tokens.

#### Scenario: First sign-in with Google
- **WHEN** a person signs in with Google for the first time
- **THEN** Core SHALL create a user, an identity for the connector and subject, a personal account, one company with the user as `owner`, and a session, and SHALL open that company's setup

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

### Requirement: Several companies behind a setting
Core SHALL serve users with several companies, each addressed explicitly. Creating another company and the company selector SHALL be available only when the multi-company setting is on.

#### Scenario: Setting off
- **WHEN** a user tries to create a second company while the setting is off
- **THEN** Core SHALL refuse it and the web UI SHALL show no selector

#### Scenario: Setting on
- **WHEN** the setting is on and a user who owns company A creates company B
- **THEN** both SHALL be listed for the user and B SHALL get its own empty data, catalog, secrets, skills and nodes

### Requirement: Company context per request
Every owner API request SHALL be resolved to exactly one company: a token's own company, or for a session the company the request names if the user is a member of it. A request naming a company the user is not a member of SHALL be answered as not found.

#### Scenario: Foreign company
- **WHEN** a user requests `/c/x/tasks` for a company `x` they are not a member of
- **THEN** Core SHALL answer not found

#### Scenario: Token with another company header
- **WHEN** a company owner token of A is sent with a header naming company B
- **THEN** Core SHALL refuse the request

### Requirement: Company roles
A membership SHALL have role `owner`, `admin` or `member`. Owners and admins SHALL manage the catalog, skills, secrets, nodes, projects and owner tokens; only owners SHALL change roles or delete the company; members SHALL work with tasks, chats and approvals, read the catalog and see secret names only. Approving an agent's catalog change or project creation SHALL need an owner or admin; a member SHALL be able to reject them.

#### Scenario: Member edits the catalog
- **WHEN** a member saves a catalog version
- **THEN** Core SHALL refuse it as forbidden

#### Scenario: Admin sets a secret
- **WHEN** an admin sets a company secret
- **THEN** Core SHALL accept it and record the admin as its author

#### Scenario: Member approves a catalog change
- **WHEN** a member approves an agent's `factory_catalog_change`
- **THEN** Core SHALL refuse it as forbidden and the approval SHALL stay pending
