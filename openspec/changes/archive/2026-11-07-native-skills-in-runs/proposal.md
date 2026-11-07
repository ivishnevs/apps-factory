Status: accepted 2026-11-06 by the owner ("оформи чендж и забирай в работу"); in implementation.

## Why

A run's skills already reach the agent natively: the node stages the agent's pinned skills (SKILL.md, scripts/,
references/) and the drivers hand them to the runtime — Claude Code reads them from `<cwd>/.claude/skills/<name>`
(its Skill tool shows the skill's base directory), Codex gets the staged root as an extra skills root
(`skills/extraRoots/set`) and lists each skill with its path. The copies are the exact hashes pinned on the run.

The agents were nevertheless told in their instructions to read skills on demand through Core
("read on demand with factory skills read …") and were given the Factory MCP tools `factory_skills_list`,
`factory_skills_search` and `factory_skills_read`. Core answers those with the text only, no path on the machine.
An agent following SKILL.md then could not find the skill's scripts: on 2026-11-06 artist-3d ran
`find / -name bx_audit.py`, which raised a burst of macOS privacy prompts (Desktop, Downloads, Photos, network
volumes) attributed to factory-node, timed out, and the agent wrote its own replacement for the skill's script.
A stopgap (`local_dir` in the `factory_skills_read` answer, commit 5ad2863) told the agent the directory; it treats
the symptom and keeps two ways to read one skill.

## What Changes

- **One way to use skills in a run: the runtime's own.** The run's instructions say the agent's skills are
  installed in its runtime (Claude Code: the Skill tool; Codex: its skills), that a skill's scripts and references
  are in the skill's directory shown when it is opened, and that the agent runs them from there and never searches
  the disk for them. The skill list (name, description) stays in the instructions.
- **No skills tools in Factory MCP for agents.** The node's Factory MCP server stops offering
  `factory_skills_list`, `factory_skills_search` and `factory_skills_read`. The stopgap `local_dir`
  (`FACTORY_SKILLS_DIR`) is removed.
- **People keep reading skills through Core.** `factory skills read AGENT SKILL [RESOURCE]` and the web library
  read from Core as before (`GET /agents/{agent}/skills…`), under the same `factory.skills.*` permissions.
- **Older nodes.** Core no longer serves the skills tools to runs; a not yet upgraded node that still offers them
  gets "unknown tool", while its agents already follow the new instructions to the native skills.

## Impact

Node: `internal/factorytools` (tool list, adapter), `internal/worker` (MCP env). Core: run instructions
(`internal/config` agent instructions). Specs: `skill-registry` (skills used natively in runs), `factory-mcp`
(no lazy skills tools for runs). Node upgrade needed for the tool removal; the instructions change with the Core
deploy.
