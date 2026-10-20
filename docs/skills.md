# Skills

Registry per company: the `skills.yaml` of the company's catalog directory, like its agents, profiles and
policies: `configs/skills.yaml` for the `default` company, `configs/<company>/skills.yaml` for another (so a
company folder is self-contained). Each skill has a path to SKILL.md, a description and categories. The path is
absolute or relative to the catalog directory holding that skills.yaml (`../skills/openspec/SKILL.md` in
`configs/`, `skills/aso/SKILL.md` for a copy in `configs/<company>/skills/aso/`); it may lead outside the factory
root. There is no fallback: a company without its own skills.yaml has no skills, and an agent there that
includes one fails loading (`unknown skill`); a company that wants a shared skill lists it in its own
skills.yaml. A registered skill whose SKILL.md is missing or invalid fails loading.

```yaml
# configs/acme/skills.yaml
skills:
  brand:
    path: skills/brand/SKILL.md                   # configs/acme/skills/brand/SKILL.md
    description: Write in Acme's brand voice.
    categories: [writing]
  openspec:
    path: ../../skills/openspec/SKILL.md          # the repository's shared copy
    description: Read project specifications and keep implementation changes aligned with OpenSpec.
    categories: [planning]
```
 SKILL.md requires YAML frontmatter with matching name and nonempty
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

Runs on a node get the offered skills materialized from the node's registry: the skills.yaml of the node's
company catalog under `skills_root` in node.yaml (`<skills_root>/configs/<company>/skills.yaml`, the same layout
Core loads from FACTORY_ROOT), verified by content hash (`skills.Hash`, `sha256:…`) against the hash pinned on the run before the
runtime starts; see [development](development.md#node-process). Core pins the hashes in the run's agent
snapshot (`skill_hashes`); a skill Core cannot hash stays unpinned. Core never sends skill content to nodes:
a node needs the company's catalog and skill files at the same relative places (an absolute path must exist on
the node too), and a copy that differs from Core's refuses the run before the runtime starts.

Core pins only hashes, not skill content: reads through Core serve the current registry files, so a running
agent can see updated resource content if registry files are changed administratively; restart Core when changing catalog metadata.

## mobile-app-testing

`skills/mobile-app-testing/SKILL.md` is the prompt guidance for iOS and Android agents (node-agent D6, 4.3): reach
app states through debug launch arguments, deep links and UI tests that find elements by accessibility identifier
(XCUITest) or test tag / resource id (Compose, Espresso, UiAutomator), never by driving the host UI, which the
mandatory `host-ui` policy denies ([policies](policies.md#command-guards)). It also names per-task simulators and
AVDs `af-<task id>` so node cleanup hooks ([development](development.md#node-process)) can remove them. Categories
`mobile`, `ios`, `android`, `testing`. A mobile agent includes it explicitly or discovers it by category:

```yaml
spec:
  skills:
    include: [openspec, mobile-app-testing]
    # or: discover: {enabled: true, categories: [ios]}
```

Existing agents (for example `calorie-ios`) are not changed; add the include to their configuration when wanted.
