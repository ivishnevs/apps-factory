# Design

`factory-mcp` is a native, stateless stdio MCP adapter. It offers tools only (initialize, ping,
tools/list, tools/call); newline JSON-RPC transport and tool schemas live in internal/factorytools.
It uses run-scoped Core JSON endpoints, not owner API credentials. Go Core owns routing, policy,
authentication, project checks, mutations, task states and audit. The adapter has no agent loop.

After claim Worker requests a credential with its run lease. Core returns an HMAC-SHA256 token
bound to run ID and its current lease, signed with the existing owner secret and domain separator.
Core checks the signature plus RUNNING/unexpired lease for every discovery/call; terminal,
CANCELLING, UNKNOWN or expired runs cannot use the credential. Token survives Core restart with
the same database and owner secret. Changing the owner secret revokes credentials. Credentials
cannot access owner/Worker APIs. No JWT library or new dependency is needed.

Worker configures `mcp_servers.factory` through official Codex CLI config overrides per process;
configuration files and user login are untouched. The required sibling factory-mcp executable
gets FACTORY_RUN_TOKEN and FACTORY_URL via env_vars. Owner/broker credentials are removed from
the runtime environment. A full-access host can still read local .env/files: this is trusted host
execution, not OS isolation. Existing third-party Codex MCP servers remain governed by Codex config.

The tool contract accepts no actor, agent_id, run_id or project_id. Core checks both captured agent
config and current config, so a running agent cannot gain permissions by reconfiguration. Domain
routes further validate rights, assignees, versions, DAGs and states. Mutation keys are required;
Core persists replay responses with mutations. Transport failures never cause automatic retry.

Structured submit during IN_PROGRESS stores a draft and preserves the writer lock. Only known
runtime completion transitions to IN_REVIEW; final text supplies a fallback when no draft exists.
Unknown/failed/cancelled runtime results do not turn drafts into reviewable work. Review requires
a distinct agent identity; priority permits a separate later review task to follow its executor.
This MVP does not introduce a wait-for-review dependency type: normal dependencies still mean DONE.

Migration 3 adds tool_calls. Insert status=0 before dispatch; finalize HTTP status afterward.
A crash may leave status=0 (unknown call outcome); domain mutation replay keys remain authoritative.
Audit records tool, request key, run/agent/project and status, not secrets or arbitrary arguments.
Owner can inspect GET /runs/{id}/tool-calls. Failed authentication isn't attributed to a trusted run.
