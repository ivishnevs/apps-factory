# Agent configuration

Core loads configs/agents/*.yaml, configs/profiles/*.yaml, configs/policies/*.yaml and
configs/skills.yaml relative to FACTORY_ROOT. Unknown YAML fields, multiple documents,
null agent/profile values, duplicate names, missing references and inheritance cycles fail loading.
Run `factory config validate`, `factory agents get demo`, or
`factory agents authorize demo factory.release.publish` to inspect resolved configuration.

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
hits its limit Core pauses every agent with that key until the reset; it defaults to the agent name.
Prompt paths must resolve inside FACTORY_ROOT, including symlinks. timeout is 1s–24h.
`execution.max_concurrent_runs` (1..8, default 1) caps the agent's live runs and `max_concurrent_runs_per_project` (default the same) its live runs in one project; dispatch and node claim check both, `factory tasks why` reports `capacity`. More than 1 needs a node: worktree tasks hold their own workspace slot, while runs in a shared project workspace still go one at a time. Keep coordinators (role manager) at 1. max_retries must be nonnegative; Task Engine applies it only to known Worker-reported failed turns.
UNKNOWN and manually reconciled failures never retry automatically. Communication create/assign permissions are checked on named-agent task mutations; can_message
checks explicit recipient agent names and reserved owner on scoped messaging. Capabilities
and context selectors remain metadata for later richer context integration.

Tools default to denylist. allowlist matches only listed operations; denylist denies matching
patterns; unrestricted introduces no allowlist. Inherited explicit denies remain restrictive.
Patterns use Go path.Match (`*` matches dotted operation names). Policy denies always win.
Approvals support never/selective/always/inherit. Explicit require patterns and mandatory policy
approval rules cannot be bypassed by never. Policy approvals express requirements; [exact owner grants](approvals.md) satisfy them at the MCP gateway without overriding deny.

`workspace.mode` selects where runs execute: `shared` (default) uses the project workspace; `worktree` gives each
task its own git worktree and branch on the node (see development.md, Task worktrees); `scratch` is for agents
that need no repository and runs in the project workspace. It is inherited from profiles like other scalars.
Nodes without worktrees enabled, and runs without a task or repository, fall back to the project workspace.

`resources` declares what one run of the agent needs on a node, per resource class, e.g. `resources: {simulator: 1}`.
Class names are lowercase like agent names; needs are at least 1. Profiles' and agents' maps merge per class. Nodes
declare capacity in node.yaml `resources` (see development.md, Runtime protocol v2); Core grants a v2 claim only
when the node's free capacity covers the needs and otherwise answers retry (capacity). Nodes do not enforce it.

## Runtime context

Context contains the prompt, deduplicated applicable policy instructions, tool/approval settings,
communication metadata and descriptions of available skills. Skill bodies are read on demand.
Core checks skill operations and named-agent task operations with the resolved policy evaluator.
Factory MCP binds agent operations to run credentials and checks captured plus current permissions.
See factory-mcp.md. Trusted agents have full host access, so shell bypass remains possible.
Configuration changes require Core restart. Stored runs retain their original composed instructions.

## Hierarchy

`spec.reports_to` names the agent's manager: another agent of the company or `owner` (the default). Validation
refuses an unknown target, a self reference and cycles; `owner` is reserved. The chain of an agent is its managers
up to the owner, its subtree the agents whose chain contains it. Every agent may message its chain (the owner
included) and its direct reports; `communication.can_message` adds peers but never removes these.

Authority over a task belongs to the owner, the agents in its assignee's chain and the assignee of the task it was
delegated from (`origin_task`). Their comments and `factory_task_direct` directives wake the task; other agents'
comments only reach its next brief. Recovery and stall escalations go to the assignee's manager (`reports_to`),
the owner for agents at the top.
