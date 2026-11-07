#!/bin/sh
# Builds the node packages handed out by Core (node-install D1):
#   factory-node-<os>-<arch>.tar.gz with bin/{factory-node,factory-shim,factory-mcp}, workers/claude (built shim and
#   its lockfile; install.sh runs npm ci on the node) and VERSION, for darwin/linux x arm64/amd64, plus SHA256SUMS.
# Usage: scripts/package-node.sh OUT_DIR [VERSION]
set -eu
OUT="${1:?usage: scripts/package-node.sh OUT_DIR [VERSION]}"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
VERSION="${2:-$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo dev)}"
PLATFORMS="${NODE_PLATFORMS:-darwin/arm64 darwin/amd64 linux/arm64 linux/amd64}"
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The Claude shim, built clean (dist may hold files of removed sources).
SHIM="$WORK/claude"
mkdir -p "$SHIM"
cp "$ROOT/workers/claude/package.json" "$ROOT/workers/claude/package-lock.json" "$ROOT/workers/claude/tsconfig.json" "$SHIM/"
cp -R "$ROOT/workers/claude/src" "$SHIM/src"
if [ -d "$ROOT/workers/claude/node_modules" ]; then
  ln -s "$ROOT/workers/claude/node_modules" "$SHIM/node_modules"
else
  (cd "$SHIM" && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error)
fi
(cd "$SHIM" && npx tsc -p . --outDir dist --rootDir . >/dev/null)
[ -f "$SHIM/dist/src/shim.js" ] || { echo "shim build failed" >&2; exit 1; }

for p in $PLATFORMS; do
  os=${p%/*}; arch=${p#*/}
  stage="$WORK/stage-$os-$arch"
  mkdir -p "$stage/bin" "$stage/workers/claude/dist/src"
  for cmd in factory-node factory-shim factory-mcp; do
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$stage/bin/$cmd" "./cmd/$cmd")
  done
  cp "$SHIM/package.json" "$SHIM/package-lock.json" "$stage/workers/claude/"
  cp "$SHIM"/dist/src/*.js "$stage/workers/claude/dist/src/"
  echo "$VERSION" > "$stage/VERSION"
  # SBOM (connector-trust 2.2): the Go modules of each binary and the shim's npm lockfile pins, in one JSON file.
  {
    printf '{"version":"%s","platform":"%s/%s","go_modules":' "$VERSION" "$os" "$arch"
    (cd "$ROOT" && go version -m "$stage/bin/factory-node" | awk '$1=="dep"||$1=="mod"{printf "%s{\"path\":\"%s\",\"version\":\"%s\"}", (n++?",":"["), $2, $3} END{if(!n)printf "["; printf "]"}')
    printf ',"npm_lockfile":"workers/claude/package-lock.json"}\n'
  } > "$stage/SBOM.json"
  tar -czf "$OUT/factory-node-$os-$arch.tar.gz" -C "$stage" VERSION SBOM.json bin workers
done

cd "$OUT"
if command -v sha256sum >/dev/null 2>&1; then sha256sum factory-node-*.tar.gz > SHA256SUMS; else shasum -a 256 factory-node-*.tar.gz > SHA256SUMS; fi
echo "$VERSION" > VERSION
ls -la "$OUT"
