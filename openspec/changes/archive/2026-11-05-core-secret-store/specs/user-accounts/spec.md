## MODIFIED Requirements

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
