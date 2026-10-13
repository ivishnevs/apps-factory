# Shared skills

Registry: configs/skills.yaml. Each skill has a path to SKILL.md relative to FACTORY_ROOT,
a description and categories. SKILL.md requires YAML frontmatter with matching name and nonempty
description; standard extra frontmatter fields are allowed. Prompt bodies are not included in run
instructions. Names in skills.include must exist. Enabled discovery adds skills matching any
configured category; disabled discovery adds none. Discovery with no categories adds none.

```sh
factory skills list demo
factory skills search demo spec documentation
factory skills read demo openspec
factory skills read demo openspec references/conventions.md
```

Owner CLI reads the authenticated Core API using FACTORY_URL/FACTORY_TOKEN. Native Codex uses
Factory MCP tools factory_skills_list/search/read with a run-scoped credential. Worker injects
only skill metadata and tool instructions, never the owner token. See factory-mcp.md.

HTTP endpoints: GET /agents/{agent}/skills (optional q/category query),
GET /agents/{agent}/skills/{skill} (optional resource query, defaults to SKILL.md).
Read is allowed only for an explicitly included or discoverable skill. All operations also check
factory.skills.list/search/read against tool filters and policies. Search results contain metadata.
Supporting files in references/, scripts/, and other skill subdirectories can be retrieved on demand.
The API returns content; it does not execute scripts. Resource paths must remain inside the skill's
own directory, including symlinks. Only regular files <= 1 MiB can be read through the API.

There is no per-run skill version snapshot yet. A running agent can see updated resource content
if registry files are changed administratively; restart Core when changing catalog metadata.
