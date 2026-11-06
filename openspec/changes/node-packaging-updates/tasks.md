## 1. Analyse

- [x] 1.1 Shim as a single executable, no Node.js on the machine (owner 2026-11-07).
- [ ] 1.3 Pick Node SEA or Bun compile: the Claude Agent SDK runs correctly, size and start time per platform.
- [ ] 1.2 Update flow: drain, switch, health check, rollback on macOS (launchd) and Linux (systemd); owner approves.

## 2. Implementation (after 1)

- [ ] 2.1 Node package without a Node.js requirement; doctor no longer checks Node.js.
- [ ] 2.2 Self-update with drain, verification, rollback; per-company automatic or with approval; channels.
- [ ] 2.3 Core: supported node version range; nodes below it get no work; web shows versions and "update".
- [ ] 2.4 `factory-node runtimes update` on request.
