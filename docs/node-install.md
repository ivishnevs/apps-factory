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

1. Checks the platform (macOS or Linux, arm64 or amd64), `curl`, `tar`, `git` (and `sha256sum` or `shasum`, part of
   every Linux and macOS). Nothing else: no Node.js, no npm (node-packaging-updates 3.3).
2. Downloads `factory-node-<os>-<arch>.tar.gz`, checks it against `SHA256SUMS` and unpacks it into
   `~/.app-factory/versions/<version>/`: `bin/factory-node` (the only Go executable: `factory-shim` and `factory-mcp`
   are links to it, and `factory-node shim` / `factory-node mcp` the same programs), `bin/factory-claude`,
   `claude-code/claude.lock` and `codex/codex.lock`. `factory-claude` is the Claude shim built as one executable with
   its Node.js runtime (Node single executable application on the official Node.js 22 build of the platform): no
   Node.js on the machine.
3. Runs `factory-node runtimes update` of that version: the node downloads the runtimes its package pins and checks
   each against its lock: the Claude Code build of the platform (the Claude Agent SDK's own platform package, sha512,
   glibc or musl on Linux) into `claude-code/claude`, where `factory-claude` finds it, and the official standalone
   Codex release binary (sha256) into `codex/codex`. A previous version's runtimes with the same pin are reused. On
   macOS it signs `factory-claude` for the machine (`codesign --sign -`), because injecting the shim invalidates the
   Node.js signature. Then `current` points at the version, and `bin`, `claude-code` and `codex` in `~/.app-factory`
   point into `current` (the versioned layout updates switch; `runtimes/` keeps the node's own logins).
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

Running it again upgrades: the new version goes next to the previous one (older ones are removed), `node.yaml` and
the keys stay, no new connection. An install from before the versioned layout loses its own `bin/`, `claude-code/`,
`workers/` and npm Codex; a node.yaml naming that npm Codex (`drivers.codex.binary`) is changed to the pinned one. A
Codex of your own stays usable with `drivers.codex.binary`. From then on nodes update from the web (below).

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
factory nodes connection ID          # name, system, address, fingerprint, status
factory nodes connect ID CODE        # approve with the terminal's code: the keys are active in this company at once
factory nodes connect ID --reject    # cancel
```

The web page `/connect/<id>` asks for the code the terminal shows; Core never shows it there, so a link someone
else sends cannot connect their machine with one click. A request lives 15 minutes, can be decided once, and gives
nothing before approval. Approve only a machine you just ran it on. The enrollment code path
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

The packages' `VERSION` is the build time in UTC with the commit, `2026.11.12.1530+6cfd893` (a `VERSION` argument
overrides it); `factory-node` carries it (`-X appfactory/internal/node.Version`) and reports it in register. The Nodes
page shows each node's version and "<release> available" when Core hands out a newer one (`GET /node-install`
`release`). Core gives no work to a node below `min_node_version` (`FACTORY_MIN_NODE_VERSION`, else the code's
minimum, none yet; a dotted version): its card says "update required" until it is reinstalled.

## Settings from the web

Owners and admins change a node's work slots, conversation slots, labels, each driver on or off and the disk
watermarks on the Nodes page (Settings on the node card) or with `PUT /nodes/{id}/config`
`{"config": {...}, "base_version": N}`; every change is a `node_config_changed` event naming who changed what. The node
gets the new version in its poll answer, fetches it, applies it without a restart (the pool grows at once, a removed
busy slot finishes its run first, a driver turned on is probed at once) and keeps it in
`<state_dir>/remote-config.json`, read over node.yaml at the next start; node.yaml itself is never rewritten. The card
shows each setting as applied, pending or refused with the reason, next to node.yaml's value; "Use node.yaml" clears
them. The machine's person decides in node.yaml:

```yaml
remote: off                  # no setting from the web; or a map keeping some local:
# remote: {labels: false, drivers: false}   # keys: slots, conversation_slots, labels, drivers, disk
slots_max: 4                 # caps the slots the web may set (0: none)
policy: {runtimes: [codex]}  # the web cannot turn on another driver
```

Sandbox, `policy`, `runtime_env`, paths, binaries and logins are never set from the web. A change that would leave
the node without a driver is refused.

The node also reports labels it detects (`macos`/`linux`, `arm64`/`amd64`, `xcode`, `ios-sim`, `headless`, `gpu`)
next to the ones the owner sets; both select agents' runs (`labels: [macos, xcode]` needs no labels in node.yaml on a
Mac with Xcode), and managers see them with `factory_nodes`.

## Updating the runtimes

The runtimes are the package's: each version directory has the Claude Code and Codex its locks pin, and an update
brings new locks and fetches the matching runtimes before it switches. `factory-node runtimes update [claude|codex]
[-home DIR] [-prev DIR]` installs them by hand (install.sh runs it): Claude Code from `claude-code/claude.lock`
(sha512), Codex from `codex/codex.lock` (sha256, the official standalone release binary; `internal/driver/codex/
codex.lock` in the repository), each file replaced atomically so running agents finish on the old one. A Codex of your
own (`drivers.codex.binary` outside the node's directory) is left alone. Logins stay as they are; restart the node
afterwards.

## Node updates

Core offers each node that is behind the release it hands out (`VERSION` of its downloads) the package for the node's
platform (`platform`, reported in register) with its sha256, in every poll and heartbeat answer (`update: {version,
url, sha256, attempt, now}`). The node, run by launchd or systemd (`FACTORY_NODE_SERVICE` in the unit) in the versioned
layout:

1. downloads the package into `versions/<v>/`, checks its sha256 and fetches the runtimes its locks pin (reusing the
   running version's when the pin is the same);
2. runs `versions/<v>/bin/factory-node selftest`: the binary starts here, reads node.yaml, reaches Core with the
   node's keys (signed `GET /health`) and passes the probes of the enabled drivers, without a model call. A failure
   ends the attempt;
3. drains: claims nothing and waits for its live runs (work and conversations) to end, without a limit;
4. switches `current` to `versions/<v>` (`update.json` records the switch) and exits with status 75; launchd
   (`KeepAlive`) or systemd (`Restart=always`) starts the new version;
5. the new version must register with a usable driver within 2 minutes (and start at most 3 times); otherwise
   `current` goes back to the previous version, which restarts and reports the failure. The previous version is kept
   until the next successful update.

The node reports each step in its heartbeat (`update: {state, version, from, reason, runs}`); the Nodes page shows it
("Updating to v: waiting for 2 live runs to end"), and Core records `node_update_started`, `node_update_failed`
(attention: feed and Telegram) and `node_updated` (register with another version). A failed attempt is not tried again
until an owner or admin presses "Retry update" (`POST /nodes/{id}/update`), which starts a new attempt.

The company chooses the policy on the Nodes page (`GET`/`PUT /settings/node-updates`, owners and admins): automatic
(the default) updates every node that is behind at its next drain; with approval a node behind the release is asked
about once (`node_update_available`: the Nodes page shows "Update to v", Telegram asks with a button) and updates once
approved. "Update now" (`POST /nodes/{id}/update {"now": true}`) stops the drain from waiting: the live runs are
interrupted like at a restart and queued again, with their work in progress saved (node-ties). One stable channel;
nodes installed before the versioned layout, run by hand, or built from the checkout (`dev`) say why they do not
update and need `install.sh` once.

## Not yet

A desktop app, Homebrew, signed and notarized binaries (connector-trust 2.2), release channels, Windows, Claude without
the JS shim (node-packaging-updates 3.5).

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
