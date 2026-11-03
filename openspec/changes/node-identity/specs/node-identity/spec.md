## ADDED Requirements

### Requirement: Node key pairs
A node SHALL generate an Ed25519 signing key pair and an X25519 encryption key pair at enrollment, keep the private keys only on the node in files readable by its user alone, and SHALL refuse to start when a private key file grants access to group or others.

#### Scenario: Loose key file
- **WHEN** a node's private key file is readable by its group
- **THEN** the node SHALL refuse to start asking for `chmod 600` and `factory node doctor` SHALL report the file

### Requirement: Globally unique node id
A `node_id` SHALL be 32 hex characters and SHALL name one node across all companies, and enrollment SHALL tell the node its company.

#### Scenario: Id of another company
- **WHEN** a node enrolls in company A with a `node_id` already used in company B
- **THEN** Core SHALL refuse the enrollment

### Requirement: Enrollment with owner confirmation
Core SHALL accept a node's public keys only with a single-use enrollment code issued for the company by its owner or admin within the last 15 minutes, SHALL keep the keys pending until an owner or admin of that company confirms the key fingerprint, and SHALL give a pending node nothing but its enrollment status. An enrolled node SHALL serve exactly that company.

#### Scenario: Code used by someone else
- **WHEN** an attacker enrolls with a stolen code before the real node
- **THEN** the real node's enrollment SHALL be refused as the code is used, the owner SHALL see a fingerprint that does not match the real node's, and rejecting it SHALL leave the attacker's keys unable to reach any node route

#### Scenario: Wrong fingerprint
- **WHEN** an owner approves a node with a fingerprint other than its pending keys'
- **THEN** Core SHALL refuse the approval and keep the keys pending

#### Scenario: Pending too long
- **WHEN** pending keys are not approved within 24 hours
- **THEN** Core SHALL delete them and the node SHALL need a new code

### Requirement: Signed node requests
Core SHALL serve node routes only to requests signed with the node's active signing key over method, path, query, body digest, creation time and nonce, and SHALL refuse requests created more than 120 seconds from Core's clock or carrying a nonce already seen for that node. A bearer token SHALL NOT reach a node route.

#### Scenario: Stolen token
- **WHEN** someone calls `POST /runs/{id}/claim` for a node with any bearer token and no valid signature
- **THEN** Core SHALL answer 401 and the run SHALL stay queued

#### Scenario: Operator and owner tokens
- **WHEN** a valid operator or owner token calls a node route such as `POST /runs/{id}/credential`
- **THEN** Core SHALL answer 401

#### Scenario: Skill blob of another company
- **WHEN** a node of company A requests a skill blob pinned only on a run of company B
- **THEN** Core SHALL answer not found

#### Scenario: Replayed claim
- **WHEN** a captured signed claim request is sent again
- **THEN** Core SHALL refuse it as a replay and emit `node.signature_refused`

### Requirement: Revocation and key rotation
Revoking a node SHALL delete its public keys, refuse its next HTTP request and end the credentials of its runs. New keys SHALL be accepted only through a request signed by the current key and confirmed by an owner or admin of the node's company, or through a new enrollment.

#### Scenario: Rotation request without the current key
- **WHEN** a request to replace a node's keys is not signed by its current signing key
- **THEN** Core SHALL refuse it and keep the current keys

#### Scenario: Revoked node with a live run
- **WHEN** an owner revokes a node while one of its runs is live
- **THEN** that run's credential SHALL be refused at once and the run SHALL follow the lost rules
