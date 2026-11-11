## 1. Owner MCP server

- [ ] 1.1 `factory mcp` (stdio) with the owner's token or session: read tools for status, pauses, nodes, why a task
      waits, runs with errors and usage explained, catalog validation with fixes, an agent's effective skills and
      prompt; tests over a fake Core.
- [ ] 1.2 Change tools (pause/resume, enrollment code, fingerprint approval, secret set, skill install) with an exact
      effect preview and owner confirmation; no secret value in any output; tests.
- [ ] 1.3 Local node tools: `doctor` result and the node log tail, read-only.

## 2. Skill factory-setup

- [ ] 2.1 Runbooks:
      - first setup;
      - moving in from Paperclip;
      - adding a machine (LAN, Tailscale `serve`);
      - node logins;
      - disk;
      - DNS and proxy faults;
      - "agent does not answer";
      - pauses;
      - token usage.
      Each runbook ends with a proving check.
- [ ] 2.2 `factory import paperclip`: projects (paused, no audit), open issues as backlog tasks, agent prompts and
      skills into the catalog, the secrets list to set; idempotent; tests against a recorded Paperclip API.

## 3. Support mode

- [ ] 3.1 `factory doctor --company`: nodes, disk, drivers and logins, failed runs by cause, stuck tasks, pending owner
      items, each with its fix or runbook; on request and as an optional Telegram digest.

## 4. Node network checks

- [ ] 4.1 `doctor` and periodic probes: `net.dns` and `net.api` resolve and reach the runtime API host and the forge
      through the system resolver; a failure disables the driver with cause and fix; tests.
- [ ] 4.2 A run ending with a network error to the API host is a node fault: retried on another node, not an attempt
      without progress; tests.

## 5. Docs and verify

- [ ] 5.1 `docs/setup-assistant.md`, `docs/owner-ui.md`, `docs/node-install.md`.
- [ ] 5.2 Replay 2026-11-11 with fakes:
      - a node with broken DNS fails `net.api` and takes no runs;
      - the owner's agent, through the MCP server, names the cause and the runbook for each case: disk critical,
        paused project, session pinned to a paused node.
