Status: raised 2026-11-06 by the owner ("can we package it without dependencies like node, and support updates
somehow"); owner decided 2026-11-07: the Claude shim ships as a single executable (option 2). Nothing implemented.

## Why

Installing a node today needs more than the node: the Claude driver runs a JavaScript shim
(`workers/claude/dist/src/shim.js`, Claude Agent SDK) under Node.js 22 or newer that the person must have, plus the
runtimes themselves (Claude Code, Codex CLI), git and gh. Updating is manual: the person (or the operator) re-runs the
installer; an upgrade in place once left the node stopped (`Bootstrap failed: 5`, fixed in the installer), and nodes
of other people (a second Mac joined on 2026-11-06) stay on old versions unless someone updates them. For customers
this is a support burden and a security risk (old binaries).

## What Changes (to analyse)

- **No Node.js to install.** Options:
  - ship a pinned Node.js runtime inside the node package, used only by the shim (simple; adds ~40 MB per platform);
  - build the shim into a single executable (Node SEA or Bun compile) shipped with the node;
  - drive Claude Code without the SDK shim (its native binary in streaming JSON mode from Go), removing JavaScript.
  **Decided (owner 2026-11-07): the single executable.** The shim is compiled into one executable per platform
  (Node SEA or Bun compile, whichever runs the Claude Agent SDK correctly) and shipped next to the node binary;
  no Node.js on the machine. Driving Claude Code directly from Go stays a possible later step.
- **Runtimes.** Claude Code and Codex stay the person's installs (their logins, `node-runtime-accounts`), but the node
  checks supported version ranges (already) and can install or update them on request (`factory-node runtimes
  update`), never silently.
- **Self-update of the node.** The node checks Core for a newer signed release (Core serves `/downloads/node` with
  SHA256SUMS; signatures from `connector-trust`), downloads and verifies it, and switches when it has no live runs
  (drain: stop claiming, wait, restart through launchd/systemd, health check, roll back to the previous version if the
  new one fails to start). The owner chooses per company: automatic (default for the stable channel) or with
  approval; the web shows each node's version and "update" for nodes behind.
- **Channels.** Stable and early (the sandbox and our own nodes on early).
- **Core side.** Supported node versions range; nodes below it get no work and a clear message.

## Impact

Packaging and release pipeline per platform (macOS arm64/x64, Linux, Windows later), the installer, the node's update
loop and service handling, the Claude driver (shim packaging or direct drive), Core's downloads and version policy,
web Nodes page. Specs: `node-install`. Related: `connector-trust` (signing), `node-runtime-accounts`, `saas-release-readiness`.
