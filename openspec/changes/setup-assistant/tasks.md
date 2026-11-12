## 1. Owner MCP server

- [x] 1.1 `factory mcp` (stdio) with the owner's token or session: read tools for status, pauses, nodes, why a task
      waits, runs with errors and usage explained, catalog validation with fixes, an agent's effective skills and
      prompt; tests over a fake Core.
      Done: `internal/ownermcp` served by `factory mcp` (owner token; `FACTORY_COMPANY` → `X-Factory-Company`; no
      browser session, see Decisions); tools `factory_status`, `factory_doctor`, `factory_why` (task, or agent+project
      for a chat), `factory_runs`, `factory_catalog_validate`, `factory_agent`, `factory_runbook`. Core adds
      `GET /doctor`, `GET /runs/recent`, `GET /agents/{agent}/effective`; wording and runbooks in `internal/support`.
      Tests: `internal/ownermcp` (fake Core), `internal/core/support_test.go` (real Core over HTTP).
- [x] 1.2 Change tools (pause/resume, enrollment code, fingerprint approval, secret set, skill install) with an exact
      effect preview and owner confirmation; no secret value in any output; tests.
      Done: a call without `confirm` answers the effect and a single-use code (HMAC over tool + arguments, 10 min);
      resume lists the tasks/runs that start; approval refuses a fingerprint that differs from Core's; secret set reads
      a file or value and never echoes it (also not in Core's error). Tests: `TestSecretNeverShown`,
      `TestConfirmationExpires`, `TestApproveChecksTheFingerprint`, `TestOwnerAgentPausedProject`.
- [x] 1.3 Local node tools: `doctor` result and the node log tail, read-only.
      Done: `factory_node_doctor` (runs `factory-node doctor`), `factory_node_log` (launchd log on macOS, journald on
      Linux, `FACTORY_NODE_LOG` override), output through the credential redactor.

## 2. Skill factory-setup

- [x] 2.1 Runbooks:
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
      Done: `skills/factory-setup/SKILL.md` + `references/<runbook>.md`, each ending with `## Proof`;
      `TestRunbooksShip` indexes the skill and checks every runbook and its proof.
- [ ] 2.2 `factory import paperclip`: projects (paused, no audit), open issues as backlog tasks, agent prompts and
      skills into the catalog, the secrets list to set; idempotent; tests against a recorded Paperclip API.
      Blocked: no Paperclip export or API format is in the repository, the docs or this change, and no recorded
      Paperclip API exists to test against. Not invented. The `moving-in-from-paperclip` runbook covers the move with
      Factory's own commands meanwhile. Needs: a Paperclip export (or API responses) of a real company.

## 3. Support mode

- [x] 3.1 `factory doctor --company`: nodes, disk, drivers and logins, failed runs by cause, stuck tasks, pending owner
      items, each with its fix or runbook; on request and as an optional Telegram digest.
      Done: Core `GET /doctor` (`support.BuildDoctor`), `factory doctor --company [--since D] [--json]`, Telegram
      `/doctor` (owners, admins) and the daily digest's "Needs fixing" (off with `/digest off`). Tests:
      `TestDoctorReplay`, `TestTelegramDoctor`.

## 4. Node network checks

- [x] 4.1 `doctor` and periodic probes: `net.dns` and `net.api` resolve and reach the runtime API host and the forge
      through the system resolver; a failure disables the driver with cause and fix; tests.
      Done: `node.Network` (fakeable resolver and dialer); Claude and Codex probes check their API host
      (`net.dns`, `net.api`), doctor and the periodic self-checks check github.com (`net.forge`). Timeouts are
      transient. Tests: `TestNetChecks`, `TestSelfChecksReachTheForge`, `TestBrokenDNSDisablesClaude`.
- [x] 4.2 A run ending with a network error to the API host is a node fault: retried on another node, not an attempt
      without progress; tests.
      Done: `protocol.FaultNetwork` from `readiness.NetworkFault` (an errno text and an API host), also after tokens
      were used; the node probes its drivers at once. Tests: `TestNetworkFault`, `TestNetworkFaultKeepsAttempts`,
      `TestNetworkFaultRechecksAtOnce`.

## 5. Docs and verify

- [x] 5.1 `docs/setup-assistant.md`, `docs/owner-ui.md`, `docs/node-install.md`.
- [x] 5.2 Replay 2026-11-11 with fakes:
      - a node with broken DNS fails `net.api` and takes no runs;
      - the owner's agent, through the MCP server, names the cause and the runbook for each case: disk critical,
        paused project, session pinned to a paused node.
      Done: `TestBrokenDNSDisablesClaude`, `TestNetworkFaultKeepsAttempts`, `TestOwnerAgentPausedProject`,
      `TestOwnerAgentNodeFaults` (disk critical, broken DNS driver, session tied to a paused node).
