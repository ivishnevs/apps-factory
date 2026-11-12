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
- [ ] 3.3 One Go binary (shim and mcp as subcommands), Codex standalone binary from `codex.lock`, runtimes managed by
      the node; install.sh needs only curl, tar, git.
- [ ] 3.4 Update flow: download and verify, self-test, drain, switch, health check, rollback; policy auto/approve.
- [x] 3.6 Detected labels (throughput-signals, 2026-11-12): the node reports what it can do (`os:macos|linux`, arch,
      `xcode`/`ios-sim`, `headless`, `gpu`) next to the labels set by the owner; the Nodes page and the manager's
      `factory_nodes` show them; agents and tasks select on them like on set labels; tests.
      Done 2026-11-12: `node.DetectLabels` at start reports `macos`/`linux`, `arm64`/`amd64`, `xcode`, `ios-sim`,
      `headless`, `gpu` as plain names (the label grammar has no ':' and agents already ask for `[macos, xcode]`);
      register and heartbeat `detected_labels`, kept apart in Core and selected on with the owner's labels (claim,
      offers, queued reasons, session holders, scheduler); the card shows them outlined; new tool `factory_nodes`
      (`TestDetectLabels`, `TestDetectedLabelsSelect`, `TestFactoryNodesTool`). Tasks select through their agent's
      labels (tasks have no labels of their own).

Blockers (2026-11-12): checking 3.1, 3.2 and 3.6 on the real nodes (the owner's Mac, hetzner-hel1) needs one manual
reinstall of each node with a package built from this change and the Core deployed; not done here (no prod access).
- [ ] 3.5 (later) Claude driven without the JS shim.
