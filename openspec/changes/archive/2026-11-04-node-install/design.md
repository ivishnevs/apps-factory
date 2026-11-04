## Context

Nodes are the customers' machines (owner decision 2026-11-03). A node is `factory-node` plus `factory-shim` (the
command guard linked under guarded command names) and `factory-mcp` (the runs' MCP adapter), found next to it
(`bin_dir`), and the Claude driver's shim `workers/claude/dist/src/shim.js` (TypeScript on the Claude Agent SDK, the
only interface with hooks, permission callbacks, MCP and interrupt; Codex has `codex app-server` and needs no shim).
The SDK pins Claude Code through a platform package (`@anthropic-ai/claude-agent-sdk-<platform>`, ~200 MB native
binary) chosen by npm at install time. Enrollment (node-identity D3) is a one-time code plus a fingerprint check.

## Goals / Non-Goals

Goals: one command from the web to a running, connected node on macOS or Linux; a person approves a node by signing
in to the web, like `claude` or `gh auth login`; the code path stays for scripts and machines without a browser.
Non-goals: a desktop app, package managers, code signing and notarization, self-update, Windows, a node without
Node.js for Claude (each later).

## Decisions

### D1. Package
`scripts/package-node.sh <out>` cross-builds the three Go binaries (`CGO_ENABLED=0`) for darwin/linux × arm64/amd64
and adds `workers/claude/{package.json,package-lock.json,dist/src/*.js}`. The layout matches the repository, so the
node's defaults (`bin_dir`, the shim under `<bin_dir>/../workers/claude`) need no settings. The packages are not
committed; the Core image builds them, so a Core always hands out nodes of its own version. Merging the three binaries
into one is not done: the shim runs under the guarded commands' names and the MCP adapter is found by process name.

### D2. Claude Code comes from npm on the node
The package does not carry Claude Code. `install.sh` runs `npm ci --omit=dev --ignore-scripts` in
`workers/claude`; the lockfile's integrity hashes pin the SDK and its platform build, npm picks the platform. Node.js
22+ is therefore required for the Claude driver; without it the node runs Codex only and the installer says so.

### D3. Downloads
`GET /install.sh` and `GET /downloads/node/<file>` (`SHA256SUMS` and the packages) need no credential. Files come
from `FACTORY_NODE_DOWNLOADS` (default `<root>/dist/node`); a missing package answers 404 with a hint. The script is
rendered with Core's address for nodes: `FACTORY_CORE_URL`, else the request's scheme and host
(`X-Forwarded-Proto`/`Host` behind the proxy).

### D4. install.sh
POSIX sh. Detects the platform; checks `curl`, `tar`, `git` (required), Node.js 22+ and `npm` (Claude), `codex`
(Codex); downloads the package and `SHA256SUMS` and checks the sum; unpacks into `${FACTORY_NODE_HOME:-~/.app-factory}`
(an upgrade replaces `bin/` and `workers/`, keeps `node.yaml` and `state/`); writes `node.yaml` once (name = host
name, `state_dir`, `bin_dir`, `projects_root` `~/AppFactory/projects`, drivers by what it found); connects
(`factory-node login`, or `enroll` with `--code`) unless the node already has a company; installs the service —
macOS: `~/Library/LaunchAgents/com.appfactory.node.plist` from `factory-node launchd`, `launchctl bootstrap
gui/<uid>`; Linux: `factory-node systemd` user unit, `systemctl --user enable --now` — and prints where the log is.
Running it again upgrades in place.

### D5. Connection requests (sign-in from the console)
1. `factory-node login` creates the keys as enroll does and sends `POST /node-connects {node_id, name, os, arch,
   sign_pub, box_pub, proof}` without a credential (rate-limited like enroll; `proof` signs
   `nodesig.ConnectMessage`). Core stores the request for 15 minutes with an 8-character code `XXXX-XXXX` and answers
   `{id, code, url, expires_at}`; `url` is `<FACTORY_PUBLIC_URL>/connect/<id>`.
2. The node opens `url` in the browser (`open`, `xdg-open`; `-no-browser` or no display: printed only), prints the
   code and polls `GET /node-connects/<id>/status`, signed by its new key.
3. The web page needs sign-in (the usual redirect), lists the companies where the person is an owner or admin, and
   shows the machine: name, system, the address Core saw, the time, the code and the fingerprint. "Connect" posts
   `POST /node-connects/<id>/approve` as the chosen company (admin route); "Cancel" posts `reject`.
4. Approval inserts the keys **active** into that company in one transaction with the feed item `node.approved`
   ("connected by sign-in"), with the same checks as enroll (id form, id and key not taken). The node learns its
   company from the status, writes it like enroll and continues.

Why this is safe enough: the request is only a public key; it gives nothing until an owner or admin, signed in,
approves it. The risk is an owner approving someone else's request sent to them as a link — the page says to approve
only a machine they just ran `factory-node login` on and shows the code printed there and the requesting address.
The same model as `gh auth login` and Tailscale. The enrollment code path (D3 of node-identity) stays unchanged.

### D6. Owner CLI now, web with the SaaS redesign
The owner decided (2026-11-04) that the web is redone for SaaS separately. This change adds the API and the owner CLI:
`factory nodes install` (`GET /node-install`: Core's node address, the command, the platforms it has),
`factory nodes connection ID` (`GET /node-connects/{id}`) and `factory nodes connect ID [--reject]`; the node prints
that command next to the page's address. For the redesign: Add a node shows the install command (sign-in) and "with
a code" for machines without a browser; `/connect/<id>` sends the person to the company they manage (or asks
which) and shows the request with Connect and Cancel.

## Risks / Trade-offs

- `curl | sh` from Core over HTTPS: the script checks the package against `SHA256SUMS` from the same Core, which
  protects from a broken download, not from a compromised Core (signing is later).
- Unsigned binaries: macOS does not quarantine files fetched with `curl`, so Gatekeeper does not block them; a
  browser download would be blocked. Notarization comes with the desktop app.
- Node.js stays a prerequisite of the Claude driver until the shim is compiled (later).
