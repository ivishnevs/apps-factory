# Agent configuration

Core loads configs/agents/*.yaml, configs/profiles/*.yaml, configs/policies/*.yaml,
configs/node-hooks/*.yaml (`NodeHooks`, served to nodes; see [development](development.md#nodehooks-company-catalog)) and
configs/skills.yaml relative to FACTORY_ROOT (for company X the same files under configs/X/, including its own
skills.yaml; see [skills](skills.md)). Unknown YAML fields, multiple documents,
null agent/profile values, duplicate names, missing references and inheritance cycles fail loading.
`metadata.preset` (`<preset>@<version>`) records the preset an agent or team was copied from ([catalog](catalog.md#presets-and-the-team-builder));
Core shows an update hint when the release has a newer one.
Run `factory config validate`, `factory agents get demo`, or
`factory agents authorize demo factory.release.publish` to inspect resolved configuration.

Core serves these files as each company's stored catalog: the repository's files seed the first version, and
from then on the catalog is changed through `factory catalog`, the web UI's Team page or the catalog API
([stored catalogs](catalog.md)); the same loader validates every saved version.

## Agents and profiles

Agents use apiVersion factory/v1 and kind Agent; profiles use kind AgentProfile.
`spec.extends` references a profile name. Multiple inheritance levels are supported; cycles fail.
Explicit child scalars override the parent, including false and zero. Nested maps merge.
Lists skills.include, policies, tools.deny and approvals.require extend and deduplicate the parent;
other lists replace. Empty additive lists do not remove inherited restrictions.
`approvals.mode: inherit` preserves the parent's mode; absent parent defaults to selective.

See configs/profiles/developer.yaml and configs/agents/demo.yaml. Supported fields are the
specification's runtime, prompt, capabilities, skills/discover, tools, approvals, communication,
context, execution and policies, plus `role` for policy applicability. Provider is currently codex.
`runtime.quota_key` (optional, a lowercase name) groups agents sharing one subscription: when a run
hits its limit Core pauses every agent with that key until the reset. Without it the agent uses its
provider's key: the one key `configs/quota_keys.yaml` declares for that provider, otherwise
`<provider>-default` (`codex-default`, `claude-default`), so agents of one provider without explicit keys pause
together and agents of the other provider keep running (runtime-drivers D5; the resolved key travels in the run's
config snapshot as `spec.runtime.default_quota_key`). While `configs/quota_keys.yaml` exists
(`quota_keys: {NAME: {provider, description}}`), an explicit key must be declared there for the agent's provider
(`factory config validate` refuses others).
`runtime.model` is the agent's model; a task may override it (`model` on the task, set by an authority over
the task through `PATCH /tasks/{id}`, `factory tasks model ID VERSION MODEL|-` or `factory_task_update`). The
override must fit the assignee's provider (Claude: `opus`, `sonnet`, `haiku`, `opusplan`, `claude-*`, optional
`[1m]`; Codex: `gpt-*`, `codex-*`, `o<digit>…`; `default` fits both) or it is refused; one left behind by a
reassignment to another provider is ignored (the agent's model runs) with a `task.model_ignored` feed event.
`runtime.effort` (`low|medium|high|xhigh|max`; Codex and Claude) is the reasoning effort (Codex `turn/start`
`effort`, Claude SDK `effort`); `runtime.max_turns` (Claude only, at least 1) caps one run's agentic turns (SDK
`maxTurns`; hitting it ends the run `max_turns`). Both are omitted from the run when unset (runtime default);
`factory config validate` refuses another value or `max_turns` on Codex.
Prompt paths must resolve inside FACTORY_ROOT, including symlinks. timeout is 1s–24h.
`execution.max_concurrent_runs` (1..8; unset: the role's default from `company.yaml` `concurrency`, below) caps the agent's live work runs and `max_concurrent_runs_per_project` (default the same) its live work runs in one project; dispatch and node claim check both, `factory tasks why` reports `capacity`. Conversation runs (the runs of the agent's `conv-<agent>-<project>` tasks) do not count against them (work-and-conversation-runs): `execution.conversation.max_concurrent_runs` (0..8, default 0 = no cap) caps them across projects, and `execution.conversation.share_work_limit: true` counts them against the work limit as before.
`runtime.context_window` is where the runtime compacts the session: one value for every run (`200k`, `1M`, a token count, or `max` for the model's maximum) or `{work: max, conversation: 200k}`; Claude and Codex accept 100k–1M, unset keeps the runtime's default. The node sets it per run from the run's kind (Claude `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, which wins over a secret setting it; Codex `model_auto_compact_token_limit`, which Codex 0.162 does not honour yet: openai/codex#16033, so leave it unset on Codex agents). The team page and the agent editor show and edit both. More than 1 needs a node: worktree tasks hold their own workspace slot, while runs in a shared project workspace still go one at a time.

`company.yaml` `concurrency` (throughput-signals) is the default work-run limit by role for agents that set no
`execution.max_concurrent_runs`: `concurrency: {developer: 2, manager: 2, default: 1}` is the built-in table, a
company's entries replace those roles and `default` covers the roles not listed (each 1..8). It limits work runs
only; conversation runs keep their own cap. The team page shows each agent's limit, whether it is the role's
default, and its live and queued runs. The team presets no longer set `max_concurrent_runs: 1` in their profiles,
so their agents take the role defaults; a company copied from a preset earlier keeps its explicit 1 until the line
is removed from its profile. max_retries must be nonnegative; it is a deprecated alias kept for compatibility: automatic retry follows the
outcome table (three attempts without progress, a daily budget; task-lifecycle.md). A `type: recovery, mode:
inspect` policy rule makes an unclear end of the agent's runs an inspection instead of a retry. Communication create/assign permissions are checked on named-agent task mutations; can_message
checks explicit recipient agent names and reserved owner on scoped messaging. Capabilities
and context selectors remain metadata for later richer context integration.

Tools default to denylist. allowlist matches only listed operations; denylist denies matching
patterns; unrestricted introduces no allowlist. Inherited explicit denies remain restrictive.
Patterns use Go path.Match (`*` matches dotted operation names). Policy denies always win.
Approvals support never/selective/always/inherit. Explicit require patterns and mandatory policy
approval rules cannot be bypassed by never. Policy approvals express requirements; [exact owner grants](approvals.md) satisfy them at the MCP gateway without overriding deny.

`workspace.mode` selects where runs execute: `shared` (default) uses the project workspace; `worktree` gives each
task its own git worktree and branch on the node (see development.md, Task worktrees); `fresh_from_default` gives
each task its own worktree without a task branch, reset to the freshly fetched default branch at every run
(release-type agents; it holds its own writer slot like `worktree` and never pins the task to a node); `scratch`
is for agents that need no repository and runs in the project workspace. It is inherited from profiles like other scalars.
Nodes without worktrees enabled, and runs without a task or repository, fall back to the project workspace.

`resources` declares what one run of the agent needs on a node, per resource class, e.g. `resources: {simulator: 1}`.
Class names are lowercase like agent names; needs are at least 1. Profiles' and agents' maps merge per class. Nodes
declare capacity in node.yaml `resources` (see development.md, Runtime protocol v2); Core grants a v2 claim only
when the node's free capacity covers the needs and otherwise answers retry (capacity). Nodes do not enforce it.
A run holds its classes until it has ended and the node has sealed it.

A task may declare classes too (throughput-signals): `resources` on `factory_task_create`/`factory_task_update` (or
`POST`/`PATCH /tasks`), e.g. `{"mac-build": 1}`, added to the agent's for every run of the task (not its
conversation). Core enforces them at claim like the agent's, so tasks needing an exclusive class take turns: a
waiting run's queued reason is `resource_busy` with the holder, e.g. `resource mac-build: 1 of 1 held, the run needs
1; held by task gate1 (dev)`. `{}` clears them.

`labels` lists node labels one run needs, e.g. `labels: [macos, xcode]`; profiles' and agents' lists merge as a
set. Core claims the run only on a node whose node.yaml `labels` include them all (otherwise retry, capacity);
`factory tasks why` says `no_capable_node` while no online node has the driver, labels and resources.

### External MCP servers

`mcp` lists external MCP servers the agent's runs get next to Factory MCP (OpenSpec `external-mcp-servers`),
e.g. `mcp: [appstoretracker]`; profiles' and agents' lists merge as a set. Claude agents only: a codex agent with
`mcp` fails loading. The servers are declared once per company in `mcp_servers.yaml` of its catalog directory
(`configs/mcp_servers.yaml`, `configs/<company>/mcp_servers.yaml`):

```yaml
mcp_servers:
  appstoretracker:
    transport: http                     # streamable HTTP, the only transport
    url: https://www.appstoretracker.com/api/mcp   # https; plain http only to 127.0.0.1/localhost (a local proxy)
    headers:
      Authorization: "Bearer ${secret:APPSTORETRACKER_API_KEY}"   # a company secret, never a literal
    deny_tools: [add_to_watchlist]      # Claude: --disallowedTools mcp__appstoretracker__add_to_watchlist
```

Header values may hold `${secret:NAME}` references and nothing else in `${...}`. A header whose name looks like a
credential (authorization, token, key, secret, cookie, password, credential, session) must reference a secret;
a literal there, an unknown server name, a bad URL or the reserved name `factory` fail loading. The run's config
snapshot carries the server with the reference unresolved; the node that runs it gets the value sealed in the
claim answer from Core's secret store (below) and resolves it there.

### Secrets

Company secrets live encrypted in Core (core-secret-store): owners and admins set them on the Secrets page or with
`factory secrets set NAME` (string on stdin) / `factory secrets set NAME --file PATH`; `factory secrets list` shows
names, kinds, versions and `used_by`, never a value. An agent's runs need the header secrets of its MCP servers and
its `spec.secrets`:

```yaml
spec:
  secrets:
    - {name: ASC_API_KEY_ID, env: ASC_KEY_ID}             # a string in the runtime's environment
    - {name: ASC_PRIVATE_KEY, env: ASC_KEY_PATH, file: true}  # a 0600 file; its path in the variable
```

Entries merge from profiles by name (the agent's replaces the profile's). Names are `[A-Za-z_][A-Za-z0-9_]*`, each
variable once and never `FACTORY_*`; a secret needed as a string and as a file is refused. A task whose agent needs a
secret that is not set (or set with the other kind) waits before dispatch with `secret_missing: NAME (agent)`: no
run, no attempt; setting it lets the task run at the next tick (`factory secrets needed`, the Secrets page). Values
reach only the runtime: environment variables, files under `<state_dir>/runs/<run>/secrets/` removed when the run
ends, and they are redacted from the run's log, events, result and attachments (long lines of a multi-line value
too).

Owners and admins give secrets to agents in the web UI (agent-secrets-ui), each change a catalog version with a note:
the agent editor's Secrets section lists the agent's own uses, those inherited from its profile and its MCP servers'
header secrets (read-only), each marked when the secret is missing or set with the other kind, and adds (from the
company's secrets; the secret's kind decides text variable or file, the variable defaults to its name) or removes own
uses; the Secrets page gives a secret to several agents at once ("Give to an agent", or when adding it) and names the
agents before a used secret is deleted. API: `GET /catalog/agents/{name}/secrets` (uses with `profile`, `server`,
`state`: set, missing, wrong_kind), `PUT /catalog/agents/{name}/secrets` (`{base, secrets}`: the agent's own uses),
`POST /secrets/{name}/agents` (`{base, agents, env}`). Core refuses a new or changed use whose secret is set with the
other kind (a file secret as a text variable, or the reverse) with a hint, there and in `PUT /catalog/agents/{name}`;
a use the agent already had stays and is marked. Agents cannot reach these routes.

## Runtime context

Context contains the prompt, deduplicated applicable policy instructions, tool/approval settings,
communication metadata and descriptions of available skills. Skills are installed in the runtime for the run and
opened natively (docs/skills.md).
Core checks skill operations and named-agent task operations with the resolved policy evaluator.
Factory MCP binds agent operations to run credentials and checks captured plus current permissions.
See factory-mcp.md. Trusted agents have full host access, so shell bypass remains possible.
Configuration changes require Core restart. Stored runs retain their original composed instructions.

## Teams

A company runs one or more teams (company-teams): `teams/<name>.yaml` next to `agents/`, and every agent names its
team with `spec.team` (required). Nodes, quota keys, secrets, skills, profiles, policies and catalog versions stay
company resources shared by all teams; a team owns its agents, its lead, its audit assignee and its projects.

```yaml
apiVersion: factory/v1
kind: Team
metadata: {name: gamedev, description: Small games for iOS}
spec:
  lead: gamedev-lead           # an agent of this team reporting to the owner: the owner's unaddressed messages
  audit_assignee: gamedev-lead # onboarding audits of this team's projects (default: the lead)
  profile: gamedev             # optional: the default extends of the team's agents that name none
```

A catalog needs at least one team; team names follow agent names. Agent names stay unique per company.

## Hierarchy

`spec.reports_to` names the agent's manager: another agent of its team, or `owner` (the default), which only the
team's lead may be: the agents reporting to the owner are exactly the team leads, one per team. Validation
refuses an unknown target, a manager in another team, a self reference and cycles; `owner` is reserved. The
chain of an agent is its managers up to the owner, its subtree the agents whose chain contains it. Every agent
may message the agents of its team and the owner; `communication.can_message` adds agents of other teams but
never removes these.

Assigning tasks follows the tree (assign-by-hierarchy): `communication.can_assign_tasks` is whom the agent may give
tasks to: `none`, `direct_reports`, `subtree` or a list of agents of its team (`true` and `false` are short for
`direct_reports` and `none`). Unset, it is `direct_reports` for an agent with reports and `none` otherwise. Core
checks the assignee of every create, assign and reassign by an agent and refuses one outside the scope, naming the
agent to give it to (the assignee's closest manager in scope: "developer-lite reports to product-manager: give the
task to product-manager"). An agent may always assign to itself; the owner and people assign to anyone. The
instructions of an agent with a scope list exactly the agents it may assign to, each with its own team, so routing
lives in the catalog rather than in prompts. A listed agent outside the team or unknown fails validation;
`direct_reports` or `subtree` on an agent without reports is a save warning. In JSON (agent snapshots sent to nodes, catalog reads) `none` and `direct_reports` are written as `false` and
`true`, which nodes built before this read as before. A manager's authority over its whole
subtree (pause, resume, cancel, reprioritize, directives) is unchanged.

```yaml
communication:
  can_assign_tasks: direct_reports   # none | direct_reports | subtree | [product-manager, app-release]
```

Creating projects (project-creation): `communication.can_create_projects` offers `factory_project_create`; unset,
it is true for the owner's direct reports (`reports_to: owner` or none, i.e. the CEO) and false for everyone else.
Set it to `false` to take the tool from a direct report, `true` to give it to another agent. A tool deny of
`factory.project.create` still wins. Every call waits for the owner's approval of the exact call, whatever the
agent's approvals settings say. The creating agent may then create tasks in its new project (`factory_task_create`
with `project_id`). The project belongs to the creating agent's team; its onboarding audit goes to that team's
`audit_assignee`. See [projects](projects.md).

```yaml
communication:
  can_create_projects: true   # default for the owner's direct reports
```

Authority over a task belongs to the owner, the agents in its assignee's chain and the assignee of the task it was
delegated from (`origin_task`). Their comments and `factory_task_direct` directives wake the task; other agents'
comments only reach its next brief. Recovery and stall escalations go to the assignee's manager (`reports_to`),
the owner for agents at the top.

Questions go up the chain. `factory_exit_ask {question, options, category}` goes to the asker's manager. The
manager answers it (`factory_message_reply`) when the category is delegated to it (`factory.decision.decide.<category>`
allowed in its policy; questions without a category it may always answer), otherwise forwards it with
`factory_question_forward {message_id, version, note}` to its own manager; the question keeps its route. The
locked categories `release`, `money`, `deletion` and `legal` go straight to the owner, and a policy that delegates
them fails validation. The answer wakes the asking task (as authority input when it comes from the chain).
A question or escalation an agent leaves unanswered for `escalate_after` moves one level up automatically (feed
event `message.auto_forwarded` names the skipped manager, and a comment on the skipped manager's conversation task
records the forward); at the owner it stays. The interval is 4 hours unless an escalation rule applying to the
recipient sets one, optionally per category (the shortest covering rule wins; 1m..168h):

```yaml
- name: lead-answers-marketing-fast
  type: escalation
  agents: [calorie-manager]
  escalate_after: 2h
  categories: [marketing]   # all categories when omitted
  instruction: Forward marketing questions promptly.
```

Authority actions (owner API/CLI, agents through Factory MCP with authority over the task; others get 403):
`factory_task_pause` / `factory_task_resume` (`POST /tasks/{id}/pause|resume`, `factory tasks pause|resume ID
VERSION [REASON]`) hold a task from new runs (`why`: `paused:task`, cleared by the pauser); with `agent` instead
of `id` they pause an agent of the actor's subtree (`POST /agents/{name}/pause|resume`). Only the pauser or a
higher authority (the owner rank 0, then depth in the tree) resumes. `factory_task_reassign` (`POST
/tasks/{id}/reassign`, `factory tasks reassign ID VERSION AGENT [REASON]`) moves a task within the actor's subtree
(the owner: anywhere); a queued run ends stale, a running one is revoked, the wait episode ends (with an active
wait), and the new assignee gets an authority `assignment` trigger. A manager's `reassign` answer to a wait
escalation is the same reassignment, to that manager. `factory_task_reprioritize` (`factory tasks reprioritize ID VERSION PRIORITY`) changes the
priority (-1000..1000). `factory_task_cancel` (`factory tasks cancel ID VERSION [REASON] [--cascade]`) cancels a
task the actor has authority over or created; open children need `cascade: true` (otherwise 409
`needs_cascade`). Pause, resume and reprioritize are not triggers: they wake nothing and leave parks (a wait with its
timer and episode, a question) as they are; cancel overrides the parks and ends the wait episode. Every action is a feed event (`task.paused`, `task.resumed`, `task.reassigned`,
`task.reprioritized`; cancelled questions are `message.cancelled`) in the one event feed: `factory watch`, the
SSE stream and `factory_feed_list` filter them like any other row (project, task, `subtree`); they carry no
attention.

Each agent talks to its boss through its conversation task ([messaging](messaging.md#conversation-tasks)). The
owner's unaddressed message in a project goes to the lead of the project's team; outside a project to the lead of
the team the owner picked (the web chat always sends one; `POST /messages` `team`), with one team to its lead;
`@name …` reaches another lead, in a team's project only that team's. `company.yaml` no longer has `owner.default_report` or `projects.audit_assignee`
(refused as unknown fields).

Managers are not woken by every event under them: a manager's brief lists its subtree's tasks that closed,
escalated or had a failed run since its last run, and its conversation wakes only for messages, questions and
escalations to it and for closed tasks it delegated.
