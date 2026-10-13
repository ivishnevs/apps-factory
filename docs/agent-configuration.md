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
Prompt paths must resolve inside FACTORY_ROOT, including symlinks. timeout is 1s–24h.
Current execution ceiling is max_concurrent_runs 1. max_retries must be nonnegative; Task Engine applies it only to known Worker-reported failed turns.
UNKNOWN and manually reconciled failures never retry automatically. Communication create/assign permissions are checked on named-agent task mutations; can_message
checks explicit recipient agent names and reserved owner on scoped messaging. Capabilities
and context selectors remain metadata for later richer context integration.

Tools default to denylist. allowlist matches only listed operations; denylist denies matching
patterns; unrestricted introduces no allowlist. Inherited explicit denies remain restrictive.
Patterns use Go path.Match (`*` matches dotted operation names). Policy denies always win.
Approvals support never/selective/always/inherit. Explicit require patterns and mandatory policy
approval rules cannot be bypassed by never. Policy approvals express requirements; [exact owner grants](approvals.md) satisfy them at the MCP gateway without overriding deny.

## Runtime context

Context contains the prompt, deduplicated applicable policy instructions, tool/approval settings,
communication metadata and descriptions of available skills. Skill bodies are read on demand.
Core checks skill operations and named-agent task operations with the resolved policy evaluator.
Factory MCP binds agent operations to run credentials and checks captured plus current permissions.
See factory-mcp.md. Trusted agents have full host access, so shell bypass remains possible.
Configuration changes require Core restart. Stored runs retain their original composed instructions.
