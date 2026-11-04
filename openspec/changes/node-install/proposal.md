Status: done (owner 2026-11-04: sign-in from the console like Claude Code, a code stays as the other way; a
console tool first, a desktop app later; the web pages wait for the SaaS redesign of the web).

## Why

A customer's node today takes a checkout of the repository: build three binaries and the Claude shim, install Node.js
and Codex, write `node.yaml`, run `factory-node enroll CODE` with `FACTORY_URL`, compare a fingerprint on the web,
render a launchd or systemd unit and load it. That is fine for the factory's developers and impossible for a SaaS
customer. Nothing hands out a node at all.

## What Changes

- **One package per platform.** `factory-node-<os>-<arch>.tar.gz` (darwin and linux, arm64 and amd64): `bin/`
  with `factory-node`, `factory-shim`, `factory-mcp`, and `workers/claude/` with the built shim and its lockfile.
  `scripts/package-node.sh` builds them with `SHA256SUMS`; the Core image carries the packages of its own version.
- **Core hands them out.** `GET /install.sh` and `GET /downloads/node/<file>`, without a credential.
- **One command installs a node.** `curl -fsSL <core>/install.sh | sh` puts the node in `~/.app-factory`, installs
  the Claude shim's dependencies with `npm ci` (the lockfile pins the Claude Code build of the platform), writes
  `node.yaml` with what it finds (`git`, `node`, `codex`), connects the node and starts it as a launchd agent or a
  systemd user service. `sh -s -- --code <code>` uses an enrollment code instead.
- **Sign-in from the console.** `factory-node login` creates the keys, asks Core for a connection request and opens
  the web page of it (or prints the address). The person signs in there, sees the machine (name, system, address
  and a short code also printed in the terminal), picks the company and approves; the node is active at once. The
  approval in the browser replaces the fingerprint comparison: the request carries the node's keys.
- **Owner CLI.** `factory nodes install` prints the command; `factory nodes connection ID` shows a request and
  `factory nodes connect ID [--reject]` decides it. The web pages (Add a node with the command, `/connect/<id>`) come
  with the SaaS redesign of the web, on the API this change adds.

## Impact

- New `node-install` capability; `node-identity` gains connection requests (ADDED).
- `internal/storage` (migration `0017_node_connects`), `internal/core` (connect and download routes),
  `internal/node` (`login`), `cmd/factory-node`, `cmd/factory` (nodes CLI), `scripts/`, `Dockerfile`. Not the web.
- Docs: `docs/node-install.md`, `docs/architecture.md`, `docs/development.md`.
- Later, not here: a desktop app, Homebrew, signed and notarized binaries, self-update, a node without Node.js.
