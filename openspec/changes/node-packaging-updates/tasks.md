## 1. Analyse

- [x] 1.1 Shim as a single executable, no Node.js on the machine (owner 2026-11-07).
- [x] 1.3 Pick Node SEA or Bun compile: Node SEA (2026-11-07). The SDK and the native Claude Code run from it on macOS
      arm64 and Linux arm64 without Node.js; built on Linux for all four platforms from the official Node.js 22.18.0
      (nodejs.org, SHASUMS256); ~110–123 MB per executable, 46–53 MB per package; no Bun toolchain needed.
- [ ] 1.2 Update flow: drain, switch, health check, rollback on macOS (launchd) and Linux (systemd); owner approves.

## 2. Implementation (after 1)

- [x] 2.1 Node package without a Node.js requirement; doctor no longer checks Node.js. Claude Code is downloaded by
      install.sh from the SDK's npm platform package pinned in `runtimes/claude.lock` (sha512); macOS signs
      `factory-claude` ad hoc on install.
- [ ] 2.2 Self-update with drain, verification, rollback; per-company automatic or with approval; channels.
- [ ] 2.3 Core: supported node version range; nodes below it get no work; web shows versions and "update".
- [ ] 2.4 `factory-node runtimes update` on request.
