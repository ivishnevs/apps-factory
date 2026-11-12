#!/bin/sh
# App Factory node installer (node-install D4), served by Core at /install.sh.
#   curl -fsSL {{{.CoreURL}}}/install.sh | sh                      # connect by signing in on the web
#   curl -fsSL {{{.CoreURL}}}/install.sh | sh -s -- --code fne_... # connect with an enrollment code
# Options: --code CODE, --name NAME, --no-service, --no-browser, --os-user NAME (Linux: install for a separate system
# user that owns nothing else, connector-trust). FACTORY_NODE_HOME (default ~/.app-factory).
# Running it again upgrades the node in place: node.yaml and the keys are kept. It needs curl, tar and git only: the
# node itself downloads the runtimes its package pins (Claude Code, the standalone Codex) and checks them.
# Layout (node-packaging-updates 3.3, 3.4): versions/<v>/ holds a version with its runtimes, current points at the one
# the service runs, bin, claude-code and codex point into current; the node updates itself by switching current.
set -eu

CORE_URL="{{{.CoreURL}}}"
HOME_DIR="${FACTORY_NODE_HOME:-$HOME/.app-factory}"
CODE=""
NAME=""
SERVICE=1
BROWSER=""
OS_USER=""
ARGS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --code) CODE="$2"; ARGS="$ARGS --code $2"; shift 2 ;;
    --name) NAME="$2"; ARGS="$ARGS --name $2"; shift 2 ;;
    --no-service) SERVICE=0; ARGS="$ARGS --no-service"; shift ;;
    --no-browser) BROWSER="-no-browser"; ARGS="$ARGS --no-browser"; shift ;;
    --os-user) OS_USER="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

say() { printf '%s\n' "$*"; }
step() { printf '\n==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) die "unsupported system $(uname -s): App Factory nodes run on macOS and Linux" ;;
esac
case "$(uname -m)" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) die "unsupported processor $(uname -m)" ;;
esac

if [ "$OS" = linux ] && [ -z "$OS_USER" ] && [ "$(id -u)" = 0 ]; then
  # node-readiness 3.1: Claude Code refuses to run agents as root, and agents should not have root on the host.
  say "This installs the node as root. Claude Code does not run agents as root, and agents should not have root here."
  say "Install it for a separate user instead (created if missing):"
  say "  curl -fsSL $CORE_URL/install.sh | sh -s -- --os-user factory$ARGS"
  exit 1
fi

if [ -n "$OS_USER" ]; then
  # A separate user (connector-trust 1.4): agents then cannot read this user's files at all.
  [ "$OS" = linux ] || die "--os-user is for Linux; on macOS create a standard user in System Settings and install there"
  [ "$(id -un)" = "$OS_USER" ] || {
    id "$OS_USER" >/dev/null 2>&1 || sudo useradd --create-home --shell /bin/sh "$OS_USER" || die "could not create user $OS_USER"
    sudo loginctl enable-linger "$OS_USER" >/dev/null 2>&1 || true
    self=$(mktemp); curl -fsSL "$CORE_URL/install.sh" -o "$self" && chmod 644 "$self"
    step "Installing as user $OS_USER"
    # sudo drops XDG_RUNTIME_DIR, and systemctl --user then cannot reach the user's manager (started by linger).
    exec sudo -u "$OS_USER" -H XDG_RUNTIME_DIR="/run/user/$(id -u "$OS_USER")" sh "$self" $ARGS
  }
fi

step "Checking this machine ($OS-$ARCH)"
for t in curl tar git; do
  command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
# node-readiness 3.1: agents commit, so the node's user needs a git identity.
if [ -z "$(git config --global user.name 2>/dev/null)" ] || [ -z "$(git config --global user.email 2>/dev/null)" ]; then
  if [ -t 1 ] && (: </dev/tty) 2>/dev/null; then
    say "  git has no identity for $(id -un); agents' commits need one"
    printf '  git user.name: '; read -r gname </dev/tty || gname=""
    printf '  git user.email: '; read -r gmail </dev/tty || gmail=""
    [ -z "$gname" ] || git config --global user.name "$gname"
    [ -z "$gmail" ] || git config --global user.email "$gmail"
  fi
  if [ -z "$(git config --global user.name 2>/dev/null)" ] || [ -z "$(git config --global user.email 2>/dev/null)" ]; then
    say "  warning: git has no identity for $(id -un); agents cannot commit until you run:"
    say "    git config --global user.name \"NAME\" && git config --global user.email EMAIL"
  fi
else
  say "  git identity: $(git config --global user.name) <$(git config --global user.email)>"
fi
# Projects may require Node.js tools (a project's requires); the node checks them per project, this is a hint.
if command -v node >/dev/null 2>&1; then
  nv=$(node --version 2>/dev/null || echo unknown)
  case "$nv" in
    v1[0-7].*|v[0-9].*) say "  Node.js $nv: older than 18; projects whose tools need a newer Node.js will show as not ready on this node" ;;
    *) say "  Node.js $nv" ;;
  esac
else
  say "  Node.js: not installed (Claude agents do not need it; projects whose tools do will show as not ready)"
fi
# The package's checksum (coreutils' sha256sum on Linux, shasum on macOS: part of every such system).
if command -v sha256sum >/dev/null 2>&1; then SUM="sha256sum"; elif command -v shasum >/dev/null 2>&1; then SUM="shasum -a 256"; else die "sha256sum or shasum is required"; fi

step "Downloading the node from $CORE_URL"
PKG="factory-node-$OS-$ARCH.tar.gz"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$CORE_URL/downloads/node/$PKG" -o "$TMP/$PKG" || die "this Core has no $PKG"
curl -fsSL "$CORE_URL/downloads/node/SHA256SUMS" -o "$TMP/SHA256SUMS" || die "this Core has no SHA256SUMS"
want=$(grep " $PKG\$" "$TMP/SHA256SUMS" | cut -d' ' -f1)
got=$($SUM "$TMP/$PKG" | cut -d' ' -f1)
[ -n "$want" ] && [ "$want" = "$got" ] || die "checksum of $PKG does not match"
mkdir -p "$TMP/pkg" "$HOME_DIR/versions"
tar -xzf "$TMP/$PKG" -C "$TMP/pkg"
V=$(cat "$TMP/pkg/VERSION" 2>/dev/null || true)
case "$V" in ""|*/*|.|..) die "the package has no valid VERSION" ;; esac
VDIR="$HOME_DIR/versions/$V"
PREV=""
[ -L "$HOME_DIR/current" ] && PREV=$(readlink "$HOME_DIR/current" | sed 's#.*/##')
# A package installed before the versioned layout: its bin, claude-code, workers and npm Codex go (runtimes/ holds the
# node's own runtime logins, node-runtime-accounts, and is never removed here).
for d in bin claude-code codex workers; do
  if [ -e "$HOME_DIR/$d" ] && [ ! -L "$HOME_DIR/$d" ]; then rm -rf "${HOME_DIR:?}/$d"; fi
done
rm -f "$HOME_DIR/VERSION" "$HOME_DIR/SBOM.json"
rm -rf "$VDIR.new"
mv "$TMP/pkg" "$VDIR.new"
if [ "$PREV" = "$V" ]; then rm -rf "${VDIR:?}.old"; mv "$VDIR" "$VDIR.old"; fi
rm -rf "$VDIR"
mv "$VDIR.new" "$VDIR"
say "  version $V in $VDIR"

step "Installing the runtimes it pins (Claude Code, Codex)"
# The node downloads them and checks each against its lock (sha512, sha256); a previous version's are reused.
RT_PREV=""
[ -n "$PREV" ] && [ -d "$HOME_DIR/versions/$PREV" ] && RT_PREV="$HOME_DIR/versions/$PREV"
[ -d "$VDIR.old" ] && RT_PREV="$VDIR.old"
CONFIG="$HOME_DIR/node.yaml"
set -- runtimes update -home "$VDIR"
[ -z "$RT_PREV" ] || set -- "$@" -prev "$RT_PREV"
[ ! -f "$CONFIG" ] || set -- "$@" -config "$CONFIG"
"$VDIR/bin/factory-node" "$@" | sed 's/^/  /' || true
rm -rf "$VDIR.old"
CLAUDE=0; [ -x "$VDIR/claude-code/claude" ] && CLAUDE=1
CODEX=0; [ -x "$VDIR/codex/codex" ] && CODEX=1
[ "$CLAUDE" = 1 ] || say "  Claude agents are off on this machine"
[ "$CODEX" = 1 ] || say "  Codex agents are off on this machine (or use your own: drivers.codex.binary in node.yaml)"
[ "$CLAUDE" = 1 ] || [ "$CODEX" = 1 ] || say "  warning: neither Claude nor Codex can run here yet; the node connects but takes no work"
if [ "$OS" = darwin ] && [ -f "$VDIR/bin/factory-claude" ]; then
  # The shim's executable is rebuilt with its script inside; sign it for this Mac (ad hoc).
  codesign --sign - --force "$VDIR/bin/factory-claude" >/dev/null 2>&1 || say "  warning: could not sign factory-claude"
fi

# Switch: current is the version the service runs; bin, claude-code and codex follow it.
ln -sfn "versions/$V" "$HOME_DIR/current"
for d in bin claude-code codex; do ln -sfn "current/$d" "$HOME_DIR/$d"; done
rm -f "$HOME_DIR/update.json"
for old in "$HOME_DIR"/versions/*; do
  b=${old##*/}
  [ "$b" = "$V" ] || [ "$b" = "$PREV" ] || rm -rf "$old"
done

STATE="$HOME_DIR/state"
if [ ! -f "$CONFIG" ]; then
  step "Writing $CONFIG"
  [ -n "$NAME" ] || NAME=$(hostname -s 2>/dev/null || hostname)
  {
    echo "# App Factory node (written by install.sh; edit freely, the installer keeps it)."
    echo "name: \"$NAME\""
    echo "state_dir: \"$STATE\""
    echo "bin_dir: \"$HOME_DIR/bin\""
    echo "projects_root: \"$HOME/AppFactory/projects\""
    echo "drivers:"
    if [ "$CODEX" = 1 ]; then echo "  codex: {enabled: true}"; else echo "  codex: {enabled: false}"; fi
    if [ "$CLAUDE" = 1 ]; then echo "  claude: {enabled: true}"; else echo "  claude: {enabled: false}"; fi
    echo "# Each task in its own git worktree, so a team's agents work in one project at once."
    echo "worktrees: {enabled: true}"
    echo "# Agents write only in their task's worktree (connector-trust); full lets them act as you everywhere."
    echo "sandbox: worktree"
  } > "$CONFIG"
elif grep -q "binary: \"$HOME_DIR/codex/node_modules/.bin/codex\"" "$CONFIG"; then
  # The npm Codex of an earlier install is gone: the node runs the standalone Codex it pins (codex/codex).
  sed "s#, *binary: \"$HOME_DIR/codex/node_modules/.bin/codex\"##" "$CONFIG" > "$CONFIG.new" && cat "$CONFIG.new" > "$CONFIG" && rm -f "$CONFIG.new"
  say "  node.yaml: Codex is now the standalone build the node pins"
fi
if [ "$OS" = linux ] && grep -q '^sandbox: *worktree' "$CONFIG" 2>/dev/null; then
  # node-readiness 3.2: Claude Code's sandbox needs bubblewrap with a user namespace and socat. Only the commands
  # are printed: this script never changes the host's packages or security settings.
  step "Checking the agent sandbox (sandbox: worktree)"
  if ! command -v bwrap >/dev/null 2>&1 || ! command -v socat >/dev/null 2>&1; then
    say "  bubblewrap or socat is missing: sandboxed agents cannot run. Install them (as an admin):"
    say "    sudo apt-get install -y bubblewrap socat     # Fedora: sudo dnf install bubblewrap socat"
  elif ! bwrap --unshare-user --ro-bind / / true >/dev/null 2>&1; then
    say "  bwrap cannot create a user namespace: sandboxed agents cannot run."
    if [ "$(sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null)" = 1 ]; then
      say "  AppArmor restricts user namespaces (Ubuntu 23.10+). Allow them for bwrap (as an admin):"
      say "    sudo tee /etc/apparmor.d/bwrap <<'PROFILE'"
      say "abi <abi/4.0>,"
      say "include <tunables/global>"
      say "profile bwrap /usr/bin/bwrap flags=(unconfined) {"
      say "  userns,"
      say "  include if exists <local/bwrap>"
      say "}"
      say "PROFILE"
      say "    sudo apparmor_parser -r /etc/apparmor.d/bwrap"
    else
      say "  Allow unprivileged user namespaces (as an admin), e.g. sudo sysctl kernel.unprivileged_userns_clone=1"
    fi
  else
    say "  bwrap and socat work"
  fi
fi
if ! command -v gitleaks >/dev/null 2>&1; then
  echo "Note: gitleaks is not installed. Task branches are scanned for secrets before they are pushed;"
  echo "      install it so agents can push: brew install gitleaks (or see https://github.com/gitleaks/gitleaks)."
fi

FN="$HOME_DIR/bin/factory-node"
export FACTORY_URL="$CORE_URL"
if [ -s "$STATE/company" ]; then
  step "Already connected to company $(cat "$STATE/company")"
elif [ -n "$CODE" ]; then
  step "Enrolling with the code"
  "$FN" enroll -config "$CONFIG" "$CODE"
else
  step "Connecting: sign in on the page that opens and approve this machine"
  "$FN" login -config "$CONFIG" $BROWSER
fi

if [ "$SERVICE" = 1 ]; then
  step "Starting the node as a service"
  cd "$HOME_DIR"
  # The service gets this PATH: runtimes need node, codex and git.
  export PATH
  if [ "$OS" = darwin ]; then
    PLIST="$HOME/Library/LaunchAgents/com.appfactory.node.plist"
    mkdir -p "$HOME/Library/LaunchAgents"
    "$FN" launchd -config "$CONFIG" > "$PLIST"
    launchctl bootout "gui/$(id -u)/com.appfactory.node" >/dev/null 2>&1 || true
    # bootout returns before the old service is gone: bootstrap fails with "5: Input/output error" until it is.
    n=0
    until launchctl bootstrap "gui/$(id -u)" "$PLIST" 2>/dev/null; do
      n=$((n + 1))
      [ "$n" -lt 10 ] || die "launchd did not start the node: run launchctl bootstrap gui/$(id -u) $PLIST"
      sleep 1
    done
    say "  launchd agent com.appfactory.node; log: $HOME/Library/Logs/factory-node.log"
  else
    CONF="${XDG_CONFIG_HOME:-$HOME/.config}"
    mkdir -p "$CONF/systemd/user" "$CONF/factory-node"
    umask 077
    echo "FACTORY_URL=$CORE_URL" > "$CONF/factory-node/env"
    umask 022
    "$FN" systemd -config "$CONFIG" > "$CONF/systemd/user/factory-node.service" 2>/dev/null
    systemctl --user daemon-reload
    systemctl --user enable --now factory-node >/dev/null
    systemctl --user restart factory-node
    say "  systemd user service factory-node; log: journalctl --user -u factory-node -f"
    # node-readiness 3.1: without linger the user's systemd (and the node) stops when the user logs out.
    if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" = yes ]; then
      say "  linger on: the node keeps running after you log out"
    elif loginctl enable-linger "$(id -un)" >/dev/null 2>&1; then
      say "  linger enabled: the node keeps running after you log out"
    else
      say "  warning: the node stops when you log out; keep it running with: sudo loginctl enable-linger $(id -un)"
    fi
  fi
else
  say ""
  say "Run it yourself: FACTORY_URL=$CORE_URL $FN run -config $CONFIG"
fi

step "Checking the node"
"$FN" doctor -config "$CONFIG" || true
[ "$CODEX" = 0 ] || [ -f "$HOME/.codex/auth.json" ] || say "Codex is not signed in: run $HOME_DIR/codex/codex login"

step "Done"
say "This machine is an App Factory node. Check it any time: FACTORY_URL=$CORE_URL $FN doctor -config $CONFIG"
