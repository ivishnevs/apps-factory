#!/bin/sh
# Builds the Claude shim as one executable per platform (node-packaging-updates 2.1): the shim bundled with the
# Claude Agent SDK (esbuild) and injected as a Node single executable application into the official Node.js binary
# of that platform, so nodes need no Node.js. Node.js archives come from nodejs.org and are checked against its
# SHASUMS256.txt.
#   scripts/build-claude-sea.sh OUT_DIR os/arch...      e.g. darwin/arm64 linux/amd64
# Writes OUT_DIR/<os>-<arch>/factory-claude. macOS binaries are ad-hoc signed when built on macOS; built elsewhere
# their signature is invalid and install.sh signs them on the node (codesign --sign -).
set -eu
OUT="${1:?usage: scripts/build-claude-sea.sh OUT_DIR os/arch...}"
shift
ROOT=$(cd "$(dirname "$0")/.." && pwd)
NODE_SEA_VERSION="${NODE_SEA_VERSION:-22.18.0}"
FUSE=NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2
SHIM_DIR="${SHIM_DIR:-$ROOT/workers/claude}"
CACHE="${NODE_SEA_CACHE:-$ROOT/.cache/node-sea}"
mkdir -p "$OUT" "$CACHE"
OUT=$(cd "$OUT" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
if command -v sha256sum >/dev/null 2>&1; then SUM="sha256sum"; else SUM="shasum -a 256"; fi

node_arch() { case "$1" in amd64) echo x64 ;; *) echo "$1" ;; esac; }

# fetch_node OS ARCH: the official Node.js binary of the platform, verified, at $CACHE/node-<os>-<arch>.
fetch_node() {
  name="node-v$NODE_SEA_VERSION-$1-$(node_arch "$2")"
  bin="$CACHE/$name/bin/node"
  if [ ! -x "$bin" ]; then
    [ -f "$CACHE/SHASUMS256-$NODE_SEA_VERSION.txt" ] || curl -fsSL "https://nodejs.org/dist/v$NODE_SEA_VERSION/SHASUMS256.txt" -o "$CACHE/SHASUMS256-$NODE_SEA_VERSION.txt"
    curl -fsSL "https://nodejs.org/dist/v$NODE_SEA_VERSION/$name.tar.gz" -o "$WORK/$name.tar.gz"
    want=$(grep " $name.tar.gz\$" "$CACHE/SHASUMS256-$NODE_SEA_VERSION.txt" | cut -d' ' -f1)
    got=$($SUM "$WORK/$name.tar.gz" | cut -d' ' -f1)
    [ -n "$want" ] && [ "$want" = "$got" ] || { echo "checksum of $name.tar.gz does not match" >&2; exit 1; }
    tar -xzf "$WORK/$name.tar.gz" -C "$CACHE" "$name/bin/node"
  fi
  echo "$bin"
}

# The blob is built by the same Node.js version that runs it, on this machine.
case "$(uname -s)" in Darwin) host_os=darwin ;; *) host_os=linux ;; esac
case "$(uname -m)" in arm64|aarch64) host_arch=arm64 ;; *) host_arch=amd64 ;; esac
HOST_NODE=$(fetch_node "$host_os" "$host_arch")

SDK_VERSION=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$SHIM_DIR/node_modules/@anthropic-ai/claude-agent-sdk/package.json" | head -1)
(cd "$SHIM_DIR" && npx esbuild src/sea.ts --bundle --platform=node --format=cjs --target=node22 --log-level=warning \
  --outfile="$WORK/shim.cjs" --define:__APPFACTORY_SDK_VERSION__="\"$SDK_VERSION\"" \
  --define:import.meta.url=__appfactory_url --banner:js='const __appfactory_url = require("node:url").pathToFileURL(__filename).href;')
printf '{"main":"%s","output":"%s","disableExperimentalSEAWarning":true,"useCodeCache":false,"useSnapshot":false}\n' "$WORK/shim.cjs" "$WORK/blob" > "$WORK/sea.json"
"$HOST_NODE" --experimental-sea-config "$WORK/sea.json" >/dev/null

for p in "$@"; do
  os=${p%/*}; arch=${p#*/}
  dest="$OUT/$os-$arch"
  mkdir -p "$dest"
  exe="$dest/factory-claude"
  cp "$(fetch_node "$os" "$arch")" "$exe"
  chmod 755 "$exe"
  if [ "$os" = darwin ] && command -v codesign >/dev/null 2>&1; then codesign --remove-signature "$exe"; fi
  macho=""
  [ "$os" = darwin ] && macho="--macho-segment-name NODE_SEA"
  # shellcheck disable=SC2086
  (cd "$SHIM_DIR" && npx postject "$exe" NODE_SEA_BLOB "$WORK/blob" --sentinel-fuse "$FUSE" $macho >/dev/null)
  if [ "$os" = darwin ] && command -v codesign >/dev/null 2>&1; then codesign --sign - --force "$exe"; fi
  echo "$exe"
done
