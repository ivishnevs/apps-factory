## MODIFIED Requirements

### Requirement: Native stdio MCP server
The native Codex runtime SHALL discover and invoke an actual stdio MCP server automatically. Task, project and agent metadata tools SHALL delegate authoritative operations to Core. The server SHALL NOT offer skills tools to runs: a run's skills reach the runtime natively (`skill-registry`).

#### Scenario: Agent lists its tasks
- **WHEN** a Codex run calls the Factory MCP task list tool
- **THEN** the server SHALL answer from Core for the run's project

#### Scenario: No skills tools
- **WHEN** a run lists its Factory MCP tools
- **THEN** no factory_skills_list, factory_skills_search or factory_skills_read SHALL be among them
