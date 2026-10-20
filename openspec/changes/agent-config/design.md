# Design

Strict YAML nodes merge profiles before typed decoding, preserving explicit false/zero overrides.
Skills, policies and deny rules are additive; other lists replace. Detect missing parents and cycles.
Policies inherit additive rules and mandatory flags. Immutable system policy protects the five
operations required by the specification; all mandatory catalog policies apply automatically.
Access precedence: deny > approval > allow. Tool allow/deny lists cannot cancel policy restrictions.
Escalation/reporting rules use the same catalog and applicability filters, rendered once into context.

Registry paths are relative to the factory root; SKILL.md and supporting resources are validated
against symlink/traversal escape. Run instructions contain metadata only. Authenticated scoped
API/CLI retrieves contents on demand; Factory MCP remains a later stage. CLI requests evaluate
factory.skills.* against the selected agent; the owner-level shared API token is not agent isolation.

Approvals are decisions, not grants: operations requiring approval remain blocked until the later
parameter-bound inbox approval workflow exists. No generic boolean bypass is introduced.
Current execution ceiling remains one run per agent, matching Foundation storage. max_retries is
configuration only until the Task Engine; no unsafe retries are introduced.

## Amendments

- 2026-10-20: the registry is per company (a4ebf62): `skills.yaml` of the company's catalog directory (`configs/skills.yaml` for `default`, `configs/<company>/skills.yaml` otherwise); SKILL.md paths are absolute or relative to that directory and may lead outside the factory root (resource reads stay confined to the skill's own directory). No fallback: a company without its own skills.yaml has no skills and including one fails loading. Nodes stage skills from their company's catalog skills.yaml under `skills_root`, so they hash the same files Core pins. This replaces "registry paths are relative to the factory root" above.
