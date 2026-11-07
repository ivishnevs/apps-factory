# The library of agents, teams and skills (default-agent-library)

App Factory ships ready agents and teams by field — software, games, growth, content, business — built from
Factory skills. People pick a ready team (Teams & agents → Library → Create this team) or add library agents to their
own team; nobody has to write prompts or find skills first.

## Presets

- `presets/agents/NAME/preset.yaml` + `prompt.md`: `metadata` (title, description, `category`, tags, `examples` —
  tasks the agent is made for, also the seeds of its eval) and `spec` (role, `agent` defaults including
  `capabilities` Does / Needs / Produces / Reviewed by, Factory `skills`, secrets it needs).
- `presets/teams/NAME.yaml`: `metadata.category`, the team, its lead (reports to the owner), agents by preset with
  managers and who may message whom, the shared profile and review rules (who reviews which role).
- The loader refuses a preset without a category from the list, an agent preset without examples or capabilities,
  unknown skills, a lead not reporting to the owner. Presets are copied into a company's catalog, never linked.

## Factory skills

- `skills/NAME/SKILL.md` with frontmatter `name`, `description`, `license` (our own skills are Apache-2.0; a
  vendored skill keeps its license and names its source in `metadata.source`), `metadata.categories` and
  `metadata.eval` — an example task and what good output contains.
- Budget: SKILL.md up to 8 KiB, the whole skill up to 64 KiB. A test enforces license, eval and size.

## Adding to the library

1. Write the skill(s) the agent needs; run `go test ./internal/presets`.
2. Write the agent preset with its capabilities and example tasks; reuse library skills.
3. For a new field, add a team preset with a lead and review rules covering every role.
4. Try the team on a real project (the pilot); adjust prompts, skills and review rules from what it shows.
