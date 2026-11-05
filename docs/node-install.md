# Installing a node (node-install)

A node is a machine of the customer that runs the company's agents. Core hands out the node of its own version and
an install script; one command installs, connects and starts it.

## On the machine

```sh
curl -fsSL https://<core>/install.sh | sh                        # connect by signing in (opens the web)
curl -fsSL https://<core>/install.sh | sh -s -- --code fne_...   # connect with an enrollment code instead
```

Options: `--name NAME` (default the host name), `--no-browser` (print the address only, for a machine without a
display: open it anywhere you are signed in), `--no-service` (do not install launchd/systemd), `FACTORY_NODE_HOME`
(default `~/.app-factory`). The owner gets the command with `factory nodes install`.

What it does:

1. Checks the platform (macOS or Linux, arm64 or amd64), `curl`, `tar`, `git`; Node.js 22+ with `npm` for Claude
   agents; without `npm`, a global `codex-cli` of the supported version for Codex agents.
2. Downloads `factory-node-<os>-<arch>.tar.gz` and checks it against `SHA256SUMS`, unpacks `bin/` (`factory-node`,
   `factory-shim`, `factory-mcp`) and `workers/claude/` into `~/.app-factory`.
3. `npm ci` in `workers/claude`: the lockfile pins the Claude Agent SDK and the Claude Code build of the platform.
   `npm install @openai/codex@<supported>` into `~/.app-factory/codex`.
4. Writes `~/.app-factory/node.yaml` once (name, `state_dir` `~/.app-factory/state`, `bin_dir`, `projects_root`
   `~/AppFactory/projects`, the drivers it could set up, `worktrees: {enabled: true}` so each task runs in its own
   git worktree and a team's agents work in one project at once). Edit it freely; the installer keeps it. Pushes of
   task branches are scanned for secrets with gitleaks first: the installer and `factory-node doctor`
   (`worktrees.scan`) say when it is missing (macOS: `brew install gitleaks`). Nodes installed before 2026-11-05
   add the `worktrees` line by hand and restart.
5. Connects, unless `state/company` exists: `factory-node login` or `factory-node enroll CODE`.
6. Starts the node: macOS `~/Library/LaunchAgents/com.appfactory.node.plist` (log `~/Library/Logs/factory-node.log`);
   Linux the user unit `factory-node` with `FACTORY_URL` in `~/.config/factory-node/env` (log `journalctl --user -u
   factory-node`; `loginctl enable-linger` keeps it running after logout).
7. Runs `factory-node doctor` and says what is missing (for example `codex login`, `claude` sign-in).

Running it again upgrades: `bin/`, `workers/` and Codex are replaced, `node.yaml` and the keys stay, no new connection.

## Connecting by signing in

`factory-node login [-config node.yaml] [-name NAME] [-no-browser]` (with `FACTORY_URL`) creates the node's keys,
sends a connection request with them and a proof, and opens `<FACTORY_PUBLIC_URL>/connect/<id>`. The terminal shows
the address, a code like `828W-A2F8` and the owner command. An owner or admin approves it for their company:

```sh
factory nodes connection ID          # name, system, address, code, fingerprint, status
factory nodes connect ID             # approve: the keys are active in this company at once
factory nodes connect ID --reject    # cancel
```

The web page for it comes with the SaaS web. A request lives 15 minutes, can be decided once, and gives nothing
before approval. Approve only a machine you just ran it on, with the same code. The enrollment code path
([development](development.md#node-identity)) stays for scripts and machines that cannot reach the web.

## Building the packages

```sh
scripts/package-node.sh dist/node [VERSION]    # darwin/linux x arm64/amd64 + SHA256SUMS (~11 MB each)
```

The Core image builds them (`Dockerfile`, stage `nodepkg`, `--build-arg NODE_VERSION=...`) into `/app/dist/node`.
Core serves `GET /install.sh` and `GET /downloads/node/<file>` without a credential from `FACTORY_NODE_DOWNLOADS`
(default `<root>/dist/node`). The script names `FACTORY_CORE_URL`, else the request's scheme and host
(`X-Forwarded-Proto`, `X-Forwarded-Host` behind a proxy). Nodes need `https` unless Core is on the same machine.

## Not yet

A desktop app, Homebrew, signed and notarized binaries, self-update, Windows, a node without Node.js for Claude.
