## MODIFIED Requirements

### Requirement: Filtered delivery and visibility
Feed events SHALL be read from the `feed_events` table and delivered to subscribers through Core's API and server-sent events. Managers SHALL see only their subtree's events and the owner SHALL see all.

#### Scenario: Lead lists events
- **WHEN** a lead lists the feed through MCP
- **THEN** only events of tasks assigned within or delegated by its subtree SHALL be returned
