Status: raised 2026-11-06 by the owner ("can we package it without dependencies like node, and support updates
somehow"); to be analysed. Nothing implemented.

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
  Recommendation to verify: a single-executable shim first; driving Claude Code directly as the long-term goal.
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
