## ADDED Requirements

### Requirement: Node key pairs
A node SHALL generate an Ed25519 signing key pair and an X25519 encryption key pair at enrollment, keep the private keys only on the node in files readable by its user alone, and SHALL refuse to start when a private key file grants access to group or others.

#### Scenario: Loose key file
- **WHEN** a node's private key file is readable by its group
- **THEN** the node SHALL refuse to start asking for `chmod 600` and `factory node doctor` SHALL report the file

### Requirement: Enrollment with owner confirmation
Core SHALL accept a node's public keys only with a single-use enrollment code issued for the company by its owner or admin within the last 15 minutes, SHALL keep the keys pending until an owner or admin of that company confirms the key fingerprint, and SHALL give a pending node nothing but its enrollment status. An enrolled node SHALL serve exactly that company.

#### Scenario: Code used by someone else
- **WHEN** an attacker enrolls with a stolen code before the real node
- **THEN** the real node's enrollment SHALL be refused as the code is used, the owner SHALL see a fingerprint that does not match the real node's, and rejecting it SHALL leave the attacker's keys unable to reach any node route

#### Scenario: Wrong fingerprint
- **WHEN** an owner approves a node with a fingerprint other than its pending keys'
- **THEN** Core SHALL refuse the approval and keep the keys pending

### Requirement: Signed node requests
Core SHALL serve node routes only to requests signed with the node's active signing key over method, path, query, body digest, creation time and nonce, and SHALL refuse requests created more than 120 seconds from Core's clock or carrying a nonce already seen for that node. A bearer token SHALL NOT reach a node route.

#### Scenario: Stolen token
- **WHEN** someone calls `POST /runs/{id}/claim` for a node with any bearer token and no valid signature
- **THEN** Core SHALL answer 401 and the run SHALL stay queued

#### Scenario: Skill blob of another company
- **WHEN** a node of company A requests a skill blob pinned only on a run of company B
- **THEN** Core SHALL answer not found

#### Scenario: Replayed claim
- **WHEN** a captured signed claim request is sent again
- **THEN** Core SHALL refuse it as a replay and emit `node.signature_refused`

### Requirement: NATS authorization by node key
A node SHALL connect to NATS with its signing key as nkey, and Core SHALL authorize the connection only for an active node, with permissions limited to its company's command subjects and its own control subject.

#### Scenario: Public key without the private key
- **WHEN** a client connects to NATS naming an active node's public key without a valid signature of the server's nonce
- **THEN** Core SHALL refuse the connection

#### Scenario: Revoked node on NATS
- **WHEN** an owner revokes a connected node
- **THEN** its NATS connection SHALL be closed and a reconnect with the same key SHALL be refused

### Requirement: Revocation and key rotation
Revoking a node SHALL delete its public keys and refuse its next HTTP request and NATS connection. New keys SHALL be accepted only through a request signed by the current key and confirmed by an owner or admin of the node's company, or through a new enrollment.

#### Scenario: Rotation request without the current key
- **WHEN** a request to replace a node's keys is not signed by its current signing key
- **THEN** Core SHALL refuse it and keep the current keys
