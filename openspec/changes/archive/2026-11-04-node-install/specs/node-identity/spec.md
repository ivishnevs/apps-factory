## ADDED Requirements

### Requirement: Connecting a node by signing in
A node SHALL be able to ask Core for a connection with its new public keys and no credential; an owner or admin who
is signed in SHALL approve it for one of their companies, which makes the keys active in that company at once, or
reject it. A request SHALL expire after 15 minutes and SHALL be usable once.

#### Scenario: Approving a connection
- **WHEN** `factory-node login` sends a connection request and an admin of company acme approves it on the web
- **THEN** the node's keys are active in acme and the node's signed status request answers acme

#### Scenario: Nothing before approval
- **WHEN** a connection request is pending, rejected or expired
- **THEN** its keys reach no node route and its status says so to the key holder only

#### Scenario: Members cannot approve
- **WHEN** a member who is not an owner or admin approves a connection request
- **THEN** Core refuses it
