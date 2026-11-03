## ADDED Requirements

### Requirement: Company secrets held by Core
Core SHALL store each company secret encrypted with that company's key, itself encrypted with a master key that is not stored in the database, and SHALL never return a secret value through any route, event or log. Only owners and admins of the company SHALL set or delete secrets; members SHALL see names only.

#### Scenario: Owner sets a key
- **WHEN** the owner sets secret APPSTORETRACKER_KEY on the website
- **THEN** Core SHALL store it encrypted and list only its name, kind, version, author and update time afterwards

#### Scenario: Ciphertext moved to another company
- **WHEN** a secret's ciphertext row is copied under another company or name in the database
- **THEN** decryption SHALL fail and the run needing it SHALL end `setup_failed`

#### Scenario: Database without the master key
- **WHEN** the database is read without Core's master key
- **THEN** no secret value SHALL be recoverable from it

### Requirement: String and file secrets
Core SHALL keep secrets of kind `string` and `file`, and an agent SHALL receive the secrets its MCP servers reference and those its `spec.secrets` names, strings as environment variables and files as owner-only files whose path is in the named variable.

#### Scenario: App Store Connect key file
- **WHEN** an agent's `spec.secrets` names file secret ASC_KEY_P8 with env `ASC_PRIVATE_KEY_PATH`
- **THEN** its run SHALL find a 0600 file with the key at that path, and the file SHALL be gone after the run ends

### Requirement: Secrets delivered sealed at claim
Core SHALL resolve the secrets a run needs at claim and deliver them only in that claim answer, each sealed to the claiming node's encryption key and bound to the run, and SHALL not store them in any run record.

#### Scenario: Any node runs the researcher
- **WHEN** a node claims a run whose MCP server needs APPSTORETRACKER_KEY held by Core
- **THEN** the run SHALL start with the key and no stored run, event, log or fixture SHALL contain its value

#### Scenario: Claim answer read in transit
- **WHEN** the claim answer is read at the proxy or written to a log
- **THEN** it SHALL hold only sealed values that open with no key but the claiming node's

#### Scenario: Secret deleted after dispatch
- **WHEN** secret ASTR_API_KEY is deleted after a run needing it was dispatched and before it is claimed
- **THEN** the run SHALL end `setup_failed` with a reason naming `ASTR_API_KEY` and no runtime SHALL be launched

### Requirement: Runs wait for missing secrets
Core SHALL dispatch a run only when every secret it needs is set. A task whose run needs a missing secret SHALL wait with a reason naming the secret and the agent, without creating a run or spending a retry, and SHALL be dispatched once the secret is set.

#### Scenario: Team created before its key
- **WHEN** a researcher needs ASTR_API_KEY, the company has not set it, and a task is assigned to the researcher
- **THEN** the task SHALL wait with reason `secret_missing: ASTR_API_KEY (researcher)` and no run SHALL be created

#### Scenario: Key set later
- **WHEN** the owner then sets ASTR_API_KEY
- **THEN** the waiting task SHALL be dispatched without any other action

### Requirement: No secrets kept on nodes
A node SHALL hold delivered values only for the run that received them and SHALL keep none on disk after the run ends; a rotated or deleted secret SHALL take effect from the next claim.

#### Scenario: Rotated key
- **WHEN** the owner replaces a secret's value
- **THEN** every run claimed afterwards SHALL receive the new value without any node action

### Requirement: Secret audit
Core SHALL record every secret write, deletion and delivery as a feed event naming the secret, actor, run and node, never its value.

#### Scenario: Delivery recorded
- **WHEN** run R on node N receives APPSTORETRACKER_KEY
- **THEN** the feed SHALL show `secret.delivered` naming the secret, R and N
