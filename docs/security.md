# Security of nodes (connector-trust)

A node is a program on your machine that runs your company's agents with your Claude and Codex subscriptions. This
page says what it can do there, what you control on the machine without Core, and how to remove it.

## What agents may touch

- `sandbox: worktree` (written by the installer for nodes installed from 2026-11-07): Codex agents run in Codex's
  `workspace-write` sandbox — they write only in their task's git worktree and its repository's `.git`, with network
  on for git and package managers; reading elsewhere is allowed by the runtime.
- `sandbox: full` (nodes installed before; also the value to set when a team needs tools outside the worktree):
  agents act as your user, as before.
- Claude agents still run with full access in both modes (worktree confinement for Claude is not done yet).

## What you control on the machine

- `factory-node pause [REASON]` stops new work at once (runs in progress finish); `factory-node resume` lifts it.
  Core cannot override it.
- `factory-node activity` lists what this machine ran, from the node's own log (`<state_dir>/activity.jsonl`).
- `factory-node accounts` shows which Claude and Codex accounts the node uses; a changed login stops that runtime
  until you accept it (see node-install).
- `factory-node uninstall` stops the service and removes the node's files (`-keep-state` keeps worktrees and
  node.yaml). Remove the node from the company on the Nodes page too.

## What the node never does

- It never sends your credentials to Core: runtimes inherit only an allowlist of environment variables, and secrets
  of the company reach a run sealed to that node.
- Pushes of task branches are scanned for secrets (gitleaks) first.

Not yet: signed and notarized binaries, published source, an external penetration test (`saas-release-readiness`).
