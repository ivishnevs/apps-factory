## 1. Analyse

- [ ] 1.1 Size and start time of the three shim options; whether the Claude Agent SDK runs as a single executable;
      whether Claude Code can be driven directly from Go with the features we use (MCP, hooks, resume, skills).
- [ ] 1.2 Update flow: drain, switch, health check, rollback on macOS (launchd) and Linux (systemd); owner approves.

## 2. Implementation (after 1)

- [ ] 2.1 Node package without a Node.js requirement; doctor no longer checks Node.js.
- [ ] 2.2 Self-update with drain, verification, rollback; per-company automatic or with approval; channels.
- [ ] 2.3 Core: supported node version range; nodes below it get no work; web shows versions and "update".
- [ ] 2.4 `factory-node runtimes update` on request.
