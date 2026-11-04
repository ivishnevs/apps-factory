## MODIFIED Requirements

### Requirement: Clients edit their whole catalog
A company's owners and admins SHALL be able to list, read, create, change and delete every document of its catalog, teams, node hooks, external MCP servers, quota keys, command guards and installed skills included, and SHALL manage the company's skill sources. Prices and built-in policies SHALL stay operator-only.

#### Scenario: Client adds a node hook
- **WHEN** a client saves a node hooks document for its company
- **THEN** that company's nodes SHALL receive it, and no other company's nodes SHALL

#### Scenario: Client adds a skill
- **WHEN** an owner adds a source and installs one of its skills
- **THEN** the skill SHALL be stored in Core and available to the company's agents without any file on the Core host
