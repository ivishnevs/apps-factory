#!/bin/sh
# App Factory node installer (node-install D4), served by Core at /install.sh.
#   curl -fsSL {{{.CoreURL}}}/install.sh | sh                      # connect by signing in on the web
#   curl -fsSL {{{.CoreURL}}}/install.sh | sh -s -- --code fne_... # connect with an enrollment code
# Options: --code CODE, --name NAME, --no-service, --no-browser, --os-user NAME (Linux: install for a separate system
# user that owns nothing else, connector-trust). FACTORY_NODE_HOME (default ~/.app-factory).
# Running it again upgrades the node in place: node.yaml and the keys are kept.
set -eu

CORE_URL="{{{.CoreURL}}}"
CODEX_VERSION="{{{.CodexVersion}}}"
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

if [ -n "$OS_USER" ]; then
  # A separate user (connector-trust 1.4): agents then cannot read this user's files at all.
  [ "$OS" = linux ] || die "--os-user is for Linux; on macOS create a standard user in System Settings and install there"
  [ "$(id -un)" = "$OS_USER" ] || {
    id "$OS_USER" >/dev/null 2>&1 || sudo useradd --create-home --shell /bin/sh "$OS_USER" || die "could not create user $OS_USER"
    sudo loginctl enable-linger "$OS_USER" >/dev/null 2>&1 || true
    self=$(mktemp); curl -fsSL "$CORE_URL/install.sh" -o "$self" && chmod 644 "$self"
    step "Installing as user $OS_USER"
    exec sudo -u "$OS_USER" -H sh "$self" $ARGS
  }
fi

step "Checking this machine ($OS-$ARCH)"
for t in curl tar git; do
  command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
if command -v sha256sum >/dev/null 2>&1; then SUM="sha256sum"; elif command -v shasum >/dev/null 2>&1; then SUM="shasum -a 256"; else die "sha256sum or shasum is required"; fi
# Claude agents need nothing installed: the package carries the shim as one executable and the pinned Claude Code
# is downloaded below (node-packaging-updates 2.1).
CLAUDE=1
if command -v sha512sum >/dev/null 2>&1; then SUM512="sha512sum"; elif command -v shasum >/dev/null 2>&1; then SUM512="shasum -a 512"; else SUM512=""; fi
[ -n "$SUM512" ] || { CLAUDE=0; say "  no sha512sum or shasum: Claude agents are off"; }
# Codex: the node supports exactly one version; npm installs it next to the node, else a matching global one.
CODEX_BIN=""
if command -v npm >/dev/null 2>&1; then
  CODEX_BIN="$HOME_DIR/codex/node_modules/.bin/codex"
  say "  Codex $CODEX_VERSION will be installed next to the node"
elif command -v codex >/dev/null 2>&1 && codex --version 2>/dev/null | grep -qx "codex-cli $CODEX_VERSION"; then
  CODEX_BIN=$(command -v codex); say "  codex: $CODEX_BIN"
else
  say "  no npm and no codex-cli $CODEX_VERSION: Codex agents are off"
fi
[ "$CLAUDE" = 1 ] || [ -n "$CODEX_BIN" ] || say "  warning: neither Claude nor Codex can run here yet; the node connects but takes no work"

step "Downloading the node from $CORE_URL"
PKG="factory-node-$OS-$ARCH.tar.gz"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$CORE_URL/downloads/node/$PKG" -o "$TMP/$PKG" || die "this Core has no $PKG"
curl -fsSL "$CORE_URL/downloads/node/SHA256SUMS" -o "$TMP/SHA256SUMS" || die "this Core has no SHA256SUMS"
want=$(grep " $PKG\$" "$TMP/SHA256SUMS" | cut -d' ' -f1)
got=$($SUM "$TMP/$PKG" | cut -d' ' -f1)
[ -n "$want" ] && [ "$want" = "$got" ] || die "checksum of $PKG does not match"
mkdir -p "$HOME_DIR"
rm -rf "$HOME_DIR/bin" "$HOME_DIR/workers" "$HOME_DIR/runtimes"
tar -xzf "$TMP/$PKG" -C "$HOME_DIR"
say "  installed in $HOME_DIR ($(cat "$HOME_DIR/VERSION" 2>/dev/null || echo unknown))"

if [ "$CLAUDE" = 1 ]; then
  step "Installing Claude Code (pinned for $OS-$ARCH)"
  libc=any
  if [ "$OS" = linux ]; then
    libc=glibc
    if ls /lib/ld-musl-* >/dev/null 2>&1; then libc=musl; fi
  fi
  lock=$(grep "^$libc " "$HOME_DIR/runtimes/claude.lock" 2>/dev/null || true)
  url=$(echo "$lock" | cut -d' ' -f2); want=$(echo "$lock" | cut -d' ' -f3)
  if [ -z "$url" ] || ! curl -fsSL "$url" -o "$TMP/claude.tgz"; then
    say "  could not download Claude Code: Claude agents are off"; CLAUDE=0
  elif [ "$($SUM512 "$TMP/claude.tgz" | cut -d' ' -f1)" != "$want" ]; then
    say "  Claude Code download does not match its checksum: Claude agents are off"; CLAUDE=0
  else
    mkdir -p "$TMP/claude" "$HOME_DIR/runtimes/claude"
    tar -xzf "$TMP/claude.tgz" -C "$TMP/claude"
    mv -f "$TMP/claude/package/claude" "$HOME_DIR/runtimes/claude/claude"
    chmod 755 "$HOME_DIR/runtimes/claude/claude"
    if [ "$OS" = darwin ]; then
      # The shim's executable is rebuilt with its script inside; sign it for this Mac (ad hoc).
      codesign --sign - --force "$HOME_DIR/bin/factory-claude" >/dev/null 2>&1 || say "  warning: could not sign factory-claude"
    fi
    say "  Claude Code $("$HOME_DIR/runtimes/claude/claude" --version 2>/dev/null | head -1)"
  fi
fi

case "$CODEX_BIN" in
  "$HOME_DIR"/codex/*)
    step "Installing Codex $CODEX_VERSION"
    if ! npm install --prefix "$HOME_DIR/codex" --no-audit --no-fund --no-update-notifier --loglevel=error "@openai/codex@$CODEX_VERSION"; then
      say "  npm install failed: Codex agents are off"; CODEX_BIN=""
    fi ;;
esac

CONFIG="$HOME_DIR/node.yaml"
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
    if [ -n "$CODEX_BIN" ]; then echo "  codex: {enabled: true, binary: \"$CODEX_BIN\"}"; else echo "  codex: {enabled: false}"; fi
    if [ "$CLAUDE" = 1 ]; then echo "  claude: {enabled: true}"; else echo "  claude: {enabled: false}"; fi
    echo "# Each task in its own git worktree, so a team's agents work in one project at once."
    echo "worktrees: {enabled: true}"
    echo "# Codex agents write only in their task's worktree (connector-trust); full lets them act as you everywhere."
    echo "sandbox: worktree"
  } > "$CONFIG"
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
    say "  to keep it running after you log out: sudo loginctl enable-linger $(id -un)"
  fi
else
  say ""
  say "Run it yourself: FACTORY_URL=$CORE_URL $FN run -config $CONFIG"
fi

step "Checking the node"
"$FN" doctor -config "$CONFIG" || true
[ -z "$CODEX_BIN" ] || [ -f "$HOME/.codex/auth.json" ] || say "Codex is not signed in: run $CODEX_BIN login"

step "Done"
say "This machine is an App Factory node. Check it any time: FACTORY_URL=$CORE_URL $FN doctor -config $CONFIG"
