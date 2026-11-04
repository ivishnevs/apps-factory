#!/bin/sh
# App Factory node installer (node-install D4), served by Core at /install.sh.
#   curl -fsSL {{{.CoreURL}}}/install.sh | sh                      # connect by signing in on the web
#   curl -fsSL {{{.CoreURL}}}/install.sh | sh -s -- --code fne_... # connect with an enrollment code
# Options: --code CODE, --name NAME, --no-service, --no-browser. FACTORY_NODE_HOME (default ~/.app-factory).
# Running it again upgrades the node in place: node.yaml and the keys are kept.
set -eu

CORE_URL="{{{.CoreURL}}}"
HOME_DIR="${FACTORY_NODE_HOME:-$HOME/.app-factory}"
CODE=""
NAME=""
SERVICE=1
BROWSER=""
while [ $# -gt 0 ]; do
  case "$1" in
    --code) CODE="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --no-service) SERVICE=0; shift ;;
    --no-browser) BROWSER="-no-browser"; shift ;;
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

step "Checking this machine ($OS-$ARCH)"
for t in curl tar git; do
  command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
if command -v sha256sum >/dev/null 2>&1; then SUM="sha256sum"; elif command -v shasum >/dev/null 2>&1; then SUM="shasum -a 256"; else die "sha256sum or shasum is required"; fi
NODE_BIN=""
CLAUDE=0
if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  major=$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)
  if [ "$major" -ge 22 ] 2>/dev/null; then
    NODE_BIN=$(command -v node); CLAUDE=1; say "  Node.js $(node --version): Claude agents can run"
  else
    say "  Node.js $(node --version) is older than 22: Claude agents are off (install Node.js 22+ and run this again)"
  fi
else
  say "  no Node.js: Claude agents are off (install Node.js 22+ and run this again)"
fi
CODEX_BIN=$(command -v codex 2>/dev/null || true)
if [ -n "$CODEX_BIN" ]; then say "  codex: $CODEX_BIN"; else say "  no codex: Codex agents are off (npm i -g @openai/codex, then codex login)"; fi
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
rm -rf "$HOME_DIR/bin" "$HOME_DIR/workers"
tar -xzf "$TMP/$PKG" -C "$HOME_DIR"
say "  installed in $HOME_DIR ($(cat "$HOME_DIR/VERSION" 2>/dev/null || echo unknown))"

if [ "$CLAUDE" = 1 ]; then
  step "Installing the Claude runtime (pinned Claude Code for $OS-$ARCH)"
  if (cd "$HOME_DIR/workers/claude" && npm ci --omit=dev --ignore-scripts --no-audit --no-fund --loglevel=error); then
    :
  else
    say "  npm ci failed: Claude agents are off"; CLAUDE=0
  fi
fi

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
    if [ "$CLAUDE" = 1 ]; then echo "  claude: {enabled: true, node: \"$NODE_BIN\"}"; else echo "  claude: {enabled: false}"; fi
  } > "$CONFIG"
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
    launchctl bootstrap "gui/$(id -u)" "$PLIST"
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

step "Done"
say "This machine is an App Factory node. Check it any time: FACTORY_URL=$CORE_URL $FN doctor -config $CONFIG"
