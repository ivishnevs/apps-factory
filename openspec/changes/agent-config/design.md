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
