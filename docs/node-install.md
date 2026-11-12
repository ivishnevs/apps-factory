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

1. Checks the platform (macOS or Linux, arm64 or amd64), `curl`, `tar`, `git`; `npm` for Codex agents (without
   `npm`, a global `codex-cli` of the supported version). Claude agents need nothing installed.
2. Downloads `factory-node-<os>-<arch>.tar.gz` and checks it against `SHA256SUMS`, unpacks `bin/` (`factory-node`,
   `factory-shim`, `factory-mcp`, `factory-claude`) and `claude-code/claude.lock` into `~/.app-factory`.
   `factory-claude` is the Claude shim built as one executable with its Node.js runtime (Node single executable
   application on the official Node.js 22 build of the platform, node-packaging-updates): no Node.js on the machine.
3. Downloads the pinned Claude Code build of the platform (the Claude Agent SDK's own platform package, URL and
   sha512 in `claude-code/claude.lock`; glibc or musl on Linux), checks its sha512 and puts the binary at
   `~/.app-factory/claude-code/claude` (not `runtimes/`, which holds the node's own logins), where `factory-claude` finds it. On macOS it signs `factory-claude` for
   the machine (`codesign --sign -`), because injecting the shim invalidates the Node.js signature.
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
   factory-node`). It enables linger for the user (`loginctl enable-linger`) so the node keeps running after
   logout, or says `sudo loginctl enable-linger USER` when it may not.
7. Runs `factory-node doctor` and says what is missing (for example `codex login`, `claude` sign-in).

Running it again upgrades: `bin/`, `workers/` and Codex are replaced, `node.yaml` and the keys stay, no new connection.

### Linux readiness (node-readiness)

- **Not as root.** Run as root without `--os-user`, the script stops and offers
  `curl -fsSL https://<core>/install.sh | sh -s -- --os-user factory` (the user is created if missing). Claude Code
  refuses to run agents as root (`--dangerously-skip-permissions cannot be used with root/sudo privileges`), and
  agents should not have root on the host. `factory-node doctor` fails the Claude driver on a root node
  (`claude.root`) with the same fix, so a root node takes no Claude runs.
- **Git identity.** Agents commit: when `git config --global user.name`/`user.email` is unset, the script asks for
  them (when it has a terminal) or prints the commands.
- **Node.js.** The script reports the Node.js version (a hint below 18); Claude agents do not need it, a project
  whose required tools do shows as not ready on the node.
- **Sandbox.** With `sandbox: worktree` (the default node.yaml), Claude Code confines runs with bubblewrap and
  socat. The script and the Claude driver probe (`claude.sandbox`) run `bwrap --unshare-user --ro-bind / / true`
  once and look for `socat`; when that fails the driver is not usable, with the cause and the fix. The script only
  prints the commands, it never changes the host's packages or security settings:

  ```sh
  sudo apt-get install -y bubblewrap socat            # Fedora: sudo dnf install bubblewrap socat
  # Ubuntu 23.10+ (kernel.apparmor_restrict_unprivileged_userns=1): let bwrap create user namespaces
  sudo tee /etc/apparmor.d/bwrap <<'PROFILE'
  abi <abi/4.0>,
  include <tunables/global>
  profile bwrap /usr/bin/bwrap flags=(unconfined) {
    userns,
    include if exists <local/bwrap>
  }
  PROFILE
  sudo apparmor_parser -r /etc/apparmor.d/bwrap
  ```

### Project readiness (node-readiness)

A connected node checks every project Core lists for it: `git ls-remote` of the project's repository as the node's
user (no prompt) and each tool the project `requires`. A node that cannot read a repository, lacks a tool, or fails
a run of the project before its agent starts (the cases above) is **not ready** for that project: the Nodes page
shows the fault and the fix on its card, Core gives it none of the project's runs, and the tasks go to other nodes
without spending their retries. Typical fixes: `gh auth login && gh auth setup-git` (Linux nodes have no
credential helper by default), sharing a private repository with the node's GitHub account, installing the tool for
the node's user. The node checks again by itself every 10 minutes; **Check again** on its card asks at once. When no
node is ready for a project, its tasks wait with `no_ready_node` and the inbox names the nodes and their fixes.

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

## A second machine and its network (setup-assistant)

A node refuses a Core URL that is not `https` unless Core is on the same machine (`http://127.0.0.1`, `localhost`).
A self-hosted Core listening on loopback is reached from another machine through:

- Tailscale (recommended): on the Core machine `tailscale serve --bg --https=443 http://127.0.0.1:8080`; the node uses
  `FACTORY_URL=https://<machine>.<tailnet>.ts.net` (a valid certificate, reachable only inside the tailnet);
- an SSH tunnel kept up by a service: `ssh -N -L 8080:127.0.0.1:8080 core-host` and `FACTORY_URL=http://127.0.0.1:8080`.

A plain LAN URL (`http://192.168.x.y:8080`) is refused on purpose.

`factory-node doctor` and the running node check the network the runtimes use, through the system resolver:
`net.dns` and `net.api` per runtime (api.anthropic.com, or `ANTHROPIC_BASE_URL`'s host, for Claude; chatgpt.com for
Codex) and `net.forge` (github.com). A failing `net.api` disables that runtime on the node with the cause and the fix,
so its runs stay queued instead of failing with `ENOTFOUND`; a run that still ends with such an error is the node's
fault and does not count against its task. Usual causes are a corporate VPN such as Cloudflare WARP, a stale Tailscale
resolver (`tailscale up --accept-dns=false`) or a proxy. Runbooks `adding-a-machine` and `dns-and-proxy` of the
`factory-setup` skill walk through both ([setup-assistant](setup-assistant.md)).

## Building the packages

```sh
scripts/package-node.sh dist/node [VERSION]    # darwin/linux x arm64/amd64 + SHA256SUMS (~11 MB each)
```

The Core image builds them (`Dockerfile`, stage `nodepkg`, `--build-arg NODE_VERSION=...`) into `/app/dist/node`.
Core serves `GET /install.sh` and `GET /downloads/node/<file>` without a credential from `FACTORY_NODE_DOWNLOADS`
(default `<root>/dist/node`). The script names `FACTORY_CORE_URL`, else the request's scheme and host
(`X-Forwarded-Proto`, `X-Forwarded-Host` behind a proxy). Nodes need `https` unless Core is on the same machine.

## Updating the runtimes

`factory-node runtimes update [claude|codex]` installs or updates the runtimes to the versions this node supports,
only when you run it: Claude Code from the package's `claude-code/claude.lock` (sha512 checked, the file replaced
atomically, so running agents finish on the old one), Codex with `npm install` into `~/.app-factory/codex` (a Codex
installed elsewhere is yours: the command says what to run). Logins stay as they are; restart the node afterwards.

## Not yet

A desktop app, Homebrew, signed and notarized binaries, self-update, Windows, Codex without npm.

## The node's own runtime logins (node-runtime-accounts)

By default a node's agents use the machine user's own Claude Code and Codex logins (`~/.claude` and the Keychain,
`~/.codex`), so logging in to another account in a terminal changes the agents' account too; `doctor` warns about it
(`claude.login`, `codex.login`). Give the node its own logins once:

```sh
factory-node login claude            # Claude Code's /login against ~/.app-factory/runtimes/claude
factory-node login codex             # codex login against ~/.app-factory/runtimes/codex
factory-node login codex -copy-current   # or start from this user's current Codex login
```

Each writes `drivers.claude.config_dir` / `drivers.codex.home` to `node.yaml` (passed to every run as
`CLAUDE_CONFIG_DIR` / `CODEX_HOME`); restart the node to use them.

Either way the node records the account each runtime is logged in to (plan, organisation, a hash; never the email)
in `<state_dir>/accounts/`. A different account fails the `claude.account` / `codex.account` check, which disables
that runtime on the node (its runs stay queued, the other runtime goes on) and reaches the owner as a node check
failure (feed, Telegram). `factory-node accounts` lists the recorded accounts; `factory-node accounts accept
claude|codex` continues with the new one from the next check.
