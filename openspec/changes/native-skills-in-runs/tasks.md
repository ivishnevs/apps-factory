## 1. Instructions

- [x] 1.1 Run instructions describe skills as installed in the runtime (Claude Skill tool / Codex skills), scripts
      and references in the skill's directory, never search the disk; tests.

## 2. Node

- [x] 2.1 Factory MCP stops offering factory_skills_list, factory_skills_search and factory_skills_read to runs.
- [x] 2.2 Remove the local_dir stopgap (FACTORY_SKILLS_DIR, Adapter.SkillsDir) and its test.

## 3. Rollout

- [ ] 3.1 Full test suite; push; upgrade the owner's node (company pause, wait for live runs).
- [ ] 3.2 Verify on a real run (Claude and Codex) that the agent opens a skill natively and runs its script from
      the skill's directory.
