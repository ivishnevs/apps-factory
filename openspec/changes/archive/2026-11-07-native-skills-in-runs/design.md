## Context

Skills are pinned per run (skill hashes in the agent snapshot) and staged by the node into a per-run root
(`$TMPDIR/factory-skills-*`, removed after the run). The Claude driver copies them into `<cwd>/.claude/skills`
(git-excluded per worktree, marked so only Factory copies are replaced); the Codex driver registers the root as an
extra skills root. A skill that cannot be downloaded or verified refuses the run before the runtime starts, so a
running agent always has every offered, pinned skill locally. Core builds the run instructions before the node
stages anything.

## Decisions

**D1 — Native skills are the only path in runs.** Both runtimes already discover skills, load SKILL.md on demand
and expose the skill's directory. A second reader (Core over MCP) returns the same text without a location and
invites the agent to look for files elsewhere. Removing it removes the failure, not just the symptom.

**D2 — The instructions name the mechanism per runtime, not a path.** The staged directory differs per run and
runtime (Claude: the worktree copy; Codex: the staged root); the runtime shows the directory when the skill is
opened. The instructions say "open it in your runtime's skills; its scripts and references are in that skill's
directory; run them from there; never search the disk for them".

**D3 — Keep Core's read endpoints for people only.** `GET /agents/{agent}/skills[/{skill}]` serves people (CLI,
web) and is permission-checked by `factory.skills.*`. The skills tools leave the shared tool definitions, so Core's
run MCP route answers "unknown tool" to a node that still offers them; such an agent already has the skills natively
and its instructions (from Core) point there.

**D4 — Revert the stopgap.** `FACTORY_SKILLS_DIR` and `local_dir` existed only to make `factory_skills_read` point
at the local copy; with the tool gone they have no reader.

## Risks

- A skill with no pinned hash (Core without a skill index) is not staged; it was never usable with its scripts
  either. The instructions list only what Core offers; behaviour unchanged.
- Agent prompts or catalog tool allow-lists that mention `factory.skills.read` keep validating; the operation still
  exists for people.
