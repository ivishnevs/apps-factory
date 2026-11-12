# Design: one-package nodes, settings from the web, updates (2026-11-12)

Asked by the owner on 2026-11-11: "do the design: maybe pack the node into one binary, or something convenient, so a
node installs on a computer without dependencies; we must be able to change the node's slots etc. in the UI and this
config propagates to the node; and node version updates". This design covers three parts. Part B adopts the idea
`node-remote-config`. Decisions marked **(owner)** wait for the owner; the rest are proposed defaults.

## Where we are

- **Package.** `factory-node-<os>-<arch>.tar.gz` contains `factory-node`, `factory-shim` and `factory-mcp` (Go), plus
  `factory-claude`, the Claude Agent SDK shim built as a Node single executable of 110–123 MB. `install.sh` downloads
  the pinned Claude Code binary (`claude.lock`, sha512) and installs Codex with `npm`. That makes `npm` the only
  outside dependency left, and only for Codex. git and gh stay the machine's own.
- **Settings.** All settings are in `node.yaml` on the machine. Changing slots means a shell on the machine and a
  restart. On 2026-11-10 the Mac and Hetzner nodes were changed this way.
- **Updates.** Re-running the installer: pause the node in the web, run `curl … | sh`, resume. The node does not
  report its own version, so Core cannot tell which nodes are behind. Only `protocol_version` and a `features` list
  are sent.

## A. One package without dependencies

1. **One Go binary.** `factory-shim` and `factory-mcp` become subcommands of `factory-node` (`factory-node shim`,
   `factory-node mcp`). It is one executable, and its other names remain as symlinks for the older layout.
2. **Codex without npm.** The node downloads the official standalone Codex binary for the platform, at the pinned
   version (`codex.lock`: URL and sha256, in the same way as `claude.lock`), into `~/.app-factory/codex/`. A Codex
   installed elsewhere stays usable (`drivers.codex.binary`). After this, `install.sh` needs only `curl`, `tar` and
   `git`.
3. **Runtimes managed by the node.** Claude Code and Codex are the node's pinned files, checked against their lock
   files. A package update brings new locks, and the node fetches the matching runtimes before it switches (see C).
   `factory-node runtimes update` stays for manual use.
4. **Later: Claude without the JS shim.** Driving Claude Code's native binary from Go (stream-json) would remove the
   110 MB `factory-claude`, leaving one binary plus the runtimes. It needs a check that hooks, MCP and permission
   callbacks work without the SDK. **(owner)** now or after updates: recommended **after**. Updates make the package
   size matter less, and this is the riskiest change of the three parts.

What the person sees: `curl -fsSL https://<core>/install.sh | sh` on a machine with git. Nothing else to install.

## B. Node settings from the web (adopts `node-remote-config`)

- **What the web can change.** These are capacity and scheduling settings:
  - `slots` and `conversation_slots`;
  - `labels`;
  - each driver's `enabled`;
  - the disk watermarks.

  They change how much the node takes on, not what it is allowed to do.
- **What stays local.** Trust and machine settings stay in `node.yaml` only:
  - sandbox and `policy`;
  - `env_allow`, paths and binaries;
  - login directories, `projects_root`.

  A compromised Core or owner account must not widen what runs on someone's machine (connector-trust).
- **Desired config in Core.** Core stores a versioned desired config per node. Owners and admins edit it on the Nodes
  page or through `PUT /nodes/{id}/config`, and every change is a feed event (who, what, which node). The poll answer
  carries `config_version`. When that is newer, the node fetches the config, validates it and applies it.
- **Layered, not rewritten.** The node writes the applied values to `<state_dir>/remote-config.json`, which is read
  over `node.yaml`. `node.yaml` stays what the person wrote. A key pinned locally (`remote: {slots: false}`, or
  `remote: off` for all keys) is refused and reported. `slots_max` in `node.yaml` caps the remote value.
- **Applied live.** The pool resizes without a restart. New slots start at once. A removed slot finishes its current
  run and then stops. The node registers its new capacity, so Core offers accordingly. Driver enable or disable
  restarts only that driver's probe.
- **Visible state.** Each setting on the node card shows `applied`, `pending` or `refused: <reason>`, and next to it
  the value from `node.yaml`.

Detected labels (throughput-signals, 2026-11-12): besides the labels the owner sets, the node detects and reports
what it can do (OS, arch, Xcode and iOS simulator, headless display, GPU), so a manager learns that the Linux node runs
the headless suites without being told; nodes reported empty labels on 2026-11-08.

## C. Version updates

- **The node reports its version.** Registration carries `version`, the package's `VERSION`. The Nodes page shows
  each node's version. Core knows the current release, because it serves the packages built in its own image.
- **Supported range.** Core keeps `min_node_version`, raised when a protocol change requires it. A node below it gets
  no work, and its card says "update required". `features` stays for finer cases, as with `context_window`.
- **How an update runs.** Core marks a node as due for an update, automatically or after the owner's approval (see
  the policy below). The poll answer carries `update: {version, url, sha256}`. The node then:
  1. downloads the package into `~/.app-factory/versions/<v>/`, checks its sha256 (signature once connector-trust
     signs releases), and fetches the runtimes the new locks name;
  2. **self-test:** runs `versions/<v>/bin/factory-node selftest`, which checks the binary starts on this machine,
     reads the config, reaches Core and runs the driver probes without a model call; a failure aborts the update and
     reports why;
  3. **drain:** stops claiming work runs and waits for its live work runs to end. Conversation runs drain too,
     because they are short. The owner can choose "update now" to interrupt; interrupted runs are re-queued, with
     their WIP saved by node-ties;
  4. **switch:** repoints `~/.app-factory/current` to `versions/<v>` and exits with a restart code; launchd or
     systemd starts the new version;
  5. **health check:** the new version must register and pass `doctor` within 2 minutes. Otherwise it switches
     `current` back to the previous version, restarts, and reports `update_failed` with the reason (feed, Telegram).
     The previous version is kept until the next successful update.
- **Policy per company (owner).**
  - `auto`: when due, update at the next drain.
  - `approve`: the Nodes page shows "Update to vN", and Telegram asks.

  Default **auto** (owner 2026-11-12); `approve` per company.
- **Channels.** **(owner)** Recommended: not now. One stable channel is enough until the SaaS release, and our own
  nodes can be updated first by hand.

## Order of work

1. C1–C2: version reporting, the version on the Nodes page, `min_node_version`. This is small and makes the rest
   visible.
2. B: settings from the web, starting with slots and conversation slots.
3. A1–A3: one binary, Codex without npm, runtimes managed by the node.
4. C3: the update flow (self-test, drain, switch, health check, rollback) and the policy.
5. A4, later: Claude driven without the JS shim.

Steps 1–3 take one more manual node update, the last one. From step 4 on, nodes update from the web.

## Owner decisions (2026-11-12)

1. **Update policy:** automatic by default; a company can switch it off and update with approval (`approve`).
2. **Drain:** wait for live runs without a limit; "update now" interrupts.
3. **Channels:** not now.
4. **Claude without the JS shim:** after the update flow, on one condition: the Claude integration must not break.
   Before switching, the Go driver must pass the same driver tests and live smokes as the shim (hooks, MCP tools,
   permissions, sessions, context window, usage), and the shim stays available as a fallback (`drivers.claude.mode`).
5. **Settings from the web:** slots, conversation slots, labels, driver enabled, disk watermarks.
