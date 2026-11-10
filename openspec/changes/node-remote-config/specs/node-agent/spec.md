## ADDED Requirements

### Requirement: Remote capacity settings (idea)
If adopted, a node SHALL pull its capacity settings (such as work and conversation slots) from Core when Core holds
a newer version, apply them without a restart where possible and report the applied version. Trust settings
(sandbox, full access, local policy, paths) SHALL stay local, and a node SHALL refuse a remote value for a setting
its local configuration pins.

#### Scenario: Owner changes conversation slots in the web
- **WHEN** the owner sets a node's conversation slots to 2 in the web
- **THEN** the node SHALL apply it at its next poll and the web SHALL show it as applied
