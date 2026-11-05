## ADDED Requirements

### Requirement: Runs confined to the project
An agent run on a connector SHALL be able to read and write only its project worktree (and the runtime's own
files) unless the machine's local policy grants full access to that project; the connector's keys, sealed secrets
and the rest of the home directory SHALL NOT be readable from a run.

#### Scenario: An agent tries to read SSH keys
- **WHEN** an agent run executes a command that reads `~/.ssh/id_ed25519`
- **THEN** the read fails and the run continues confined to its worktree

### Requirement: The machine has the last word
The connector SHALL refuse any task its local policy does not allow, Core SHALL NOT be able to change that policy,
update the connector or run anything outside a run, and the person SHALL be able to pause or remove the connector
locally at any time.

#### Scenario: A project outside the allowed folders
- **WHEN** Core asks the connector to run a task for a project outside the folders its policy lists
- **THEN** the connector refuses, nothing runs, and the web shows the refusal with the machine's reason

### Requirement: No inbound access
The connector SHALL open no listening port and accept no remote shell; it SHALL only make outgoing HTTPS requests
signed with its own key.

#### Scenario: Port scan
- **WHEN** someone scans a machine running the connector
- **THEN** no port of the connector is open
