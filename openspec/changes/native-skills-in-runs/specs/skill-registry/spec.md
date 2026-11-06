## ADDED Requirements

### Requirement: Skills used natively in runs
A run SHALL use its skills only through its runtime's own skills mechanism, from the copies the node staged for the
run: Claude Code reads them as project skills, Codex as an extra skills root. The run's instructions SHALL list the
agent's skills and say that a skill's scripts and references are in the skill's directory shown when it is opened,
to be run from there, and never searched for elsewhere on the machine.

#### Scenario: Skill with a script
- **WHEN** an agent follows a skill whose SKILL.md runs `scripts/audit.py`
- **THEN** it SHALL find the script in that skill's directory of its run, with no disk search

#### Scenario: People read a skill
- **WHEN** a person runs `factory skills read AGENT SKILL` or opens the skill in the web library
- **THEN** Core SHALL serve the content as before, under the `factory.skills.*` permissions
