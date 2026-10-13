# Factory MCP requirements

- The native Codex runtime discovers and invokes an actual stdio MCP server automatically.
- Task/project/agent metadata and lazy skills tools delegate authoritative operations to Core.
- Mutations require explicit stable request keys and versions where applicable.
- Run credentials bind identity to an active lease and cannot access owner routes or another project.
- Current and captured policies, communication permissions, version and state checks apply per call.
- Submit cannot release a running project's writer; self-review is forbidden.
- Calls and their outcomes persist across restart; terminal and expired tokens are rejected.
- Missing approval grants fail closed with 423; no implicit approval is granted.
- Real smoke must demonstrate manager-created/assigned tasks, executor MCP submit, manager acceptance.
- Messaging/inbox, decision records and approval grants are explicitly outside this change.
