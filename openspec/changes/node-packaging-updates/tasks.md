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
- [x] 2.2 Self-update with drain, verification, rollback; per-company automatic or with approval; channels.
      Done 2026-11-12 with 3.4. Channels: not now (owner decision 3): one stable release, the one Core hands out.
- [x] 2.3 Core: supported node version range; nodes below it get no work; web shows versions and "update".
      Done 2026-11-12 with 3.1: `min_node_version` (code minimum, none yet; `FACTORY_MIN_NODE_VERSION`), no offers,
      claims refused `update_required`, queued reason `update_required`, `node_update_required` event; the card shows
      the version, "update required" and "<release> available" (`GET /node-install` `release`). The "update" action
      itself comes with the update flow (3.4).
- [x] 2.4 `factory-node runtimes update` on request (Claude Code from claude.lock, Codex with npm into the node's
      directory; a Codex installed elsewhere gets the command to run).

## 3. Design 2026-11-12 (after the owner's answers to design.md)

- [x] 3.1 Nodes report `version`; Nodes page shows it; Core `min_node_version` (below it: no work, "update required").
      Done 2026-11-12: packages are versioned by build time (`2026.11.12.1530+<commit>`, `scripts/package-node.sh`,
      `-X appfactory/internal/node.Version`, else the package's `VERSION` file); register carries `version`
      (migration 0038); `protocol.CompareVersions` orders dotted versions, `dev` is never below; Core enforces the
      minimum in offers, claim, queued reasons and the scheduler's node verdicts (`TestMinNodeVersion`).
- [x] 3.2 Settings from the web (node-remote-config): desired config per node, poll `config_version`, layered
      `remote-config.json`, local pins and `slots_max`, live pool resize, applied/pending/refused on the card.
      Done 2026-11-12: `node_configs` (version, who, when), `GET/PUT /nodes/{id}/config` (owners and admins,
      `base_version`, `node_config_changed` event), `config_version` in the poll (a change wakes it) and heartbeat
      answers, the node fetches it signed, `node.ApplyRemote` over node.yaml (`remote: off|{key: false}`,
      `slots_max` caps, `policy.runtimes` and "no driver left" refuse), `<state_dir>/remote-config.json` read at start,
      live `Pool.Resize` (busy removed slots finish first), drivers turned on or off with only their probe, disk gate
      watermarks, heartbeat `capacity` and `config` report (`node_config_refused` once per version); Nodes page
      Settings panel with node.yaml's value and applied/pending/refused per setting (`TestSettingsFromTheWebRealCore`,
      `TestApplyRemote`, `TestPoolResize`, `TestNodeConfig`, `TestNodeConfigRoutes`). Settings: slots, conversation
      slots, labels, driver enabled, disk watermarks (owner decision 5).
- [x] 3.3 One Go binary (shim and mcp as subcommands), Codex standalone binary from `codex.lock`, runtimes managed by
      the node; install.sh needs only curl, tar, git.
      Done 2026-11-12: `cmd/factory-shim` and `cmd/factory-mcp` are gone; `factory-node shim` / `factory-node mcp`, and
      run as factory-shim (or a guarded command linked to it) / factory-mcp it is that program (`TestPersonality`, the
      worker and driver tests run the shim and MCP adapter through links); the package and `make build` link
      factory-shim and factory-mcp to factory-node. `internal/driver/codex/codex.lock` pins the official standalone
      Codex 0.162.1 per platform (URL, sha256, archive entry; `TestCodexLock`), copied into each package as
      `codex/codex.lock`; `internal/node/runtimes` installs Claude Code (sha512) and Codex (sha256) from the locks into
      the package directory, reusing the previous version's file at the same pin (`TestCodexFromLock`,
      `TestClaudeFromLock` against a fake release server); the node runs `codex/codex` unless node.yaml or CODEX_BIN
      names another (`node.CodexBinary`). install.sh needs curl, tar and git (sha256sum/shasum of the system): it
      unpacks into `versions/<v>`, runs `factory-node runtimes update -home -prev`, points `current`, `bin`,
      `claude-code`, `codex` at it, keeps the previous version, removes a pre-layout install's own dirs and npm Codex
      and rewrites node.yaml's npm Codex binary (`TestInstallScriptVersionedLayout` runs it against a fake Core).
- [x] 3.4 Update flow: download and verify, self-test, drain, switch, health check, rollback; policy auto/approve.
      Done 2026-11-12: Core offers a node behind its release the package of the node's `platform` (register) with
      its sha256 in every poll and heartbeat answer (`update: {version, url, sha256, attempt, now}`); policy per
      company `auto` (default) / `approve` (`GET/PUT /settings/node-updates`, the Nodes page), approval and "update now"
      with `POST /nodes/{id}/update` (owners and admins) and the Telegram button of `node_update_available`; a failed
      attempt is not offered again until a retry (new attempt). The node (`runner.Updater`, `internal/node/selfupdate`)
      downloads into `versions/<v>`, checks the sha256, fetches the runtimes, runs `factory-node selftest` (binary,
      node.yaml, Core with the node's keys, the enabled drivers' probes; no model call), drains (no claims, waits for
      live runs without a limit; "update now" stops waiting and the restart interrupts them), switches `current`,
      exits 75 for launchd/systemd (`FACTORY_NODE_SERVICE` in the units) and the new version must register with a
      usable driver within 2 minutes and 3 starts, else it switches back and the previous version reports
      `update_failed`; events `node_update_started`, `node_update_failed` (attention), `node_updated`; the Nodes page
      shows the step, "Update to v", "Update now", "Retry update" and the policy (`TestNodeUpdateRealCore`,
      `TestNodeUpdateNowAndWithdraw`, `TestNodeUpdateHealthDeadline`, `TestNodeUpdateRoutes`, `TestDecideNodeUpdate`,
      `TestUnpackSwitchLocate`, `TestHealthCheckRollback`). Migration 0040 (0039 is reserved by another branch).
- [x] 3.6 Detected labels (throughput-signals, 2026-11-12): the node reports what it can do (`os:macos|linux`, arch,
      `xcode`/`ios-sim`, `headless`, `gpu`) next to the labels set by the owner; the Nodes page and the manager's
      `factory_nodes` show them; agents and tasks select on them like on set labels; tests.
      Done 2026-11-12: `node.DetectLabels` at start reports `macos`/`linux`, `arm64`/`amd64`, `xcode`, `ios-sim`,
      `headless`, `gpu` as plain names (the label grammar has no ':' and agents already ask for `[macos, xcode]`);
      register and heartbeat `detected_labels`, kept apart in Core and selected on with the owner's labels (claim,
      offers, queued reasons, session holders, scheduler); the card shows them outlined; new tool `factory_nodes`
      (`TestDetectLabels`, `TestDetectedLabelsSelect`, `TestFactoryNodesTool`). Tasks select through their agent's
      labels (tasks have no labels of their own).

Blockers (2026-11-12): checking 3.1, 3.2, 3.3, 3.4 and 3.6 on the real nodes (the owner's Mac, hetzner-hel1) needs
one manual reinstall of each node with a package built from this change (the last manual one: the versioned layout)
and the Core deployed, then one release later to watch a real update (launchd and systemd restarting into the new
version, a rollback); not done here (no prod access, no real nodes). The Codex pins in `codex.lock` are the GitHub
release's published sha256 digests of rust-v0.162.1; the darwin-arm64 archive was downloaded and matches (entry
`codex-aarch64-apple-darwin`), the other three are not downloaded here and the binaries were not run on a machine.
Release signatures (verified by the node before the self-test) come with connector-trust 2.2 (Apple account).
- [ ] 3.5 (later) Claude driven without the JS shim. Not started: the owner made it conditional (decision 4) on the
      Go driver passing the shim's driver tests and live smokes after the update flow, with the shim kept as a
      fallback (`drivers.claude.mode`).
