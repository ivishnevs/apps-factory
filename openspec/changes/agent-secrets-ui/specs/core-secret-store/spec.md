## ADDED Requirements

### Requirement: Granting secrets to agents from the web
The web UI SHALL let owners and admins give a company secret to an agent and take it away, from the agent's editor
and from the Secrets page, by writing the agent's `spec.secrets` as a catalog change. It SHALL show each agent's own
and inherited secret uses with their variable names and kinds, mark uses whose secret is missing or of the wrong
kind, refuse a file secret as a string variable, and warn with the agents' names before a used secret is deleted.
Secret values SHALL never be shown.

#### Scenario: Give a file secret to an agent
- **WHEN** the owner gives the file secret `GAME_SERVER_SSH_KEY` to network-dev from its editor
- **THEN** the catalog SHALL get a new version with that use and the next network-dev run SHALL find the key file at
  the path in `GAME_SERVER_SSH_KEY`

#### Scenario: Delete a used secret
- **WHEN** the owner deletes a secret that two agents use
- **THEN** the page SHALL name both agents and ask to confirm before deleting
