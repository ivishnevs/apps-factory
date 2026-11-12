## 1. Analyse

- [x] 1.1 Shim as a single executable, no Node.js on the machine (owner 2026-11-07).
- [x] 1.3 Pick Node SEA or Bun compile: Node SEA (2026-11-07). The SDK and the native Claude Code run from it on macOS
      arm64 and Linux arm64 without Node.js; built on Linux for all four platforms from the official Node.js 22.18.0
      (nodejs.org, SHASUMS256); ~110–123 MB per executable, 46–53 MB per package; no Bun toolchain needed.
- [x] 1.2 Update flow: drain, switch, health check, rollback on macOS (launchd) and Linux (systemd); owner approves.
      Designed 2026-11-12 in design.md (with settings from the web and the one-package layout); its questions wait
      for the owner.

## 2. Implementation (after 1)

- [x] 2.1 Node package without a Node.js requirement; doctor no longer checks Node.js. Claude Code is downloaded by
      install.sh from the SDK's npm platform package pinned in `claude-code/claude.lock` (sha512); macOS signs
      `factory-claude` ad hoc on install.
- [ ] 2.2 Self-update with drain, verification, rollback; per-company automatic or with approval; channels.
- [ ] 2.3 Core: supported node version range; nodes below it get no work; web shows versions and "update".
- [x] 2.4 `factory-node runtimes update` on request (Claude Code from claude.lock, Codex with npm into the node's
      directory; a Codex installed elsewhere gets the command to run).

## 3. Design 2026-11-12 (after the owner's answers to design.md)

- [ ] 3.1 Nodes report `version`; Nodes page shows it; Core `min_node_version` (below it: no work, "update required").
- [ ] 3.2 Settings from the web (node-remote-config): desired config per node, poll `config_version`, layered
      `remote-config.json`, local pins and `slots_max`, live pool resize, applied/pending/refused on the card.
- [ ] 3.3 One Go binary (shim and mcp as subcommands), Codex standalone binary from `codex.lock`, runtimes managed by
      the node; install.sh needs only curl, tar, git.
- [ ] 3.4 Update flow: download and verify, self-test, drain, switch, health check, rollback; policy auto/approve.
- [ ] 3.6 Detected labels (throughput-signals, 2026-11-12): the node reports what it can do (`os:macos|linux`, arch,
      `xcode`/`ios-sim`, `headless`, `gpu`) next to the labels set by the owner; the Nodes page and the manager's
      `factory_nodes` show them; agents and tasks select on them like on set labels; tests.
- [ ] 3.5 (later) Claude driven without the JS shim.
