## ADDED Requirements

### Requirement: Assignment follows the reporting tree
An agent SHALL assign tasks only within its `can_assign_tasks` scope (`none`, `direct_reports`, `subtree` or an explicit list), defaulting to `direct_reports` for an agent with reports and `none` otherwise. Core SHALL refuse a create, assign or reassign outside the scope and name the agent to use instead. The owner and admins SHALL assign to anyone.

#### Scenario: CEO assigns past the PM
- **WHEN** the CEO, with scope `direct_reports`, creates a task for developer-lite, who reports to the Product Manager
- **THEN** Core SHALL refuse it and name product-manager as the agent to give it to

#### Scenario: PM assigns its developer
- **WHEN** the Product Manager creates a task for developer-lite
- **THEN** Core SHALL accept it

### Requirement: Roster within the scope
An agent's instructions SHALL list only the agents it may assign to, each with what its own subtree covers.

#### Scenario: CEO's roster
- **WHEN** the CEO's instructions are built
- **THEN** they SHALL list its direct reports, with the Product Manager's entry naming developer, developer-lite, product-designer and reviewer as its team, and SHALL NOT list developer-lite on its own
