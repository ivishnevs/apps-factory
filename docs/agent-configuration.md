# Agent configuration

Core loads configs/agents/*.yaml, configs/profiles/*.yaml, configs/policies/*.yaml,
configs/node-hooks/*.yaml (`NodeHooks`, served to nodes; see [development](development.md#nodehooks-company-catalog)) and
configs/skills.yaml relative to FACTORY_ROOT (for company X the same files under configs/X/, including its own
skills.yaml; see [skills](skills.md)). Unknown YAML fields, multiple documents,
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
`execution.max_concurrent_runs` (1..8, default 1) caps the agent's live runs and `max_concurrent_runs_per_project` (default the same) its live runs in one project; dispatch and node claim check both, `factory tasks why` reports `capacity`. More than 1 needs a node: worktree tasks hold their own workspace slot, while runs in a shared project workspace still go one at a time. Keep coordinators (role manager) at 1. max_retries must be nonnegative; it is a deprecated alias kept for compatibility: automatic retry follows the
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

`labels` lists node labels one run needs, e.g. `labels: [macos, xcode]`; profiles' and agents' lists merge as a
set. Core claims the run only on a node whose node.yaml `labels` include them all (otherwise retry, capacity);
`factory tasks why` says `no_capable_node` while no online node has the driver, labels and resources.

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

Each agent talks to its boss through its conversation task ([messaging](messaging.md#conversation-tasks)). With
more than one agent reporting to the owner, `configs/company.yaml` must name the default one, which gets the
owner's unaddressed messages (`@name …` reaches another):

```yaml
owner:
  default_report: manager
```

Managers are not woken by every event under them: a manager's brief lists its subtree's tasks that closed,
escalated or had a failed run since its last run, and its conversation wakes only for messages, questions and
escalations to it and for closed tasks it delegated.
