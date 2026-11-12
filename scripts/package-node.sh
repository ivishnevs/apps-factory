#!/bin/sh
# Builds the node packages handed out by Core (node-install D1):
#   factory-node-<os>-<arch>.tar.gz with bin/factory-node (the only Go executable; factory-shim and factory-mcp link to
#   it), bin/factory-claude, claude-code/claude.lock and codex/codex.lock (the pinned Claude Code and standalone Codex the
#   node downloads and verifies: factory-node runtimes update, node-packaging-updates A2-A3) and VERSION, for
#   darwin/linux x arm64/amd64, plus SHA256SUMS. factory-claude is the Claude shim as one executable
#   (node-packaging-updates 2.1): no Node.js on nodes.
# Usage: scripts/package-node.sh OUT_DIR [VERSION]
set -eu
OUT="${1:?usage: scripts/package-node.sh OUT_DIR [VERSION]}"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
# VERSION orders releases (node-packaging-updates C: Core's min_node_version compares them): the build time in UTC,
# YYYY.MM.DD.HHMM, and the commit when known (2026.11.12.1530+6cfd893). The node binary carries it too.
VERSION="${2:-$(date -u +%Y.%m.%d.%H%M)$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null | sed 's/^/+/')}"
PLATFORMS="${NODE_PLATFORMS:-darwin/arm64 darwin/amd64 linux/arm64 linux/amd64}"
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The Claude shim's dependencies (esbuild and postject build the single executable).
SHIM="$WORK/claude"
mkdir -p "$SHIM"
cp "$ROOT/workers/claude/package.json" "$ROOT/workers/claude/package-lock.json" "$ROOT/workers/claude/tsconfig.json" "$SHIM/"
cp -R "$ROOT/workers/claude/src" "$SHIM/src"
if [ -d "$ROOT/workers/claude/node_modules" ]; then
  ln -s "$ROOT/workers/claude/node_modules" "$SHIM/node_modules"
else
  (cd "$SHIM" && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error)
fi
(cd "$SHIM" && npx tsc -p . --noEmit)
SHIM_DIR="$SHIM" "$ROOT/scripts/build-claude-sea.sh" "$WORK/sea" $PLATFORMS >/dev/null

# claude_lock OS ARCH: the pinned Claude Code package of the platform (the SDK's optional dependency in the
# shim's lockfile) as "libc url sha512-hex" lines; Linux has a glibc and a musl build.
claude_lock() {
  node -e '
    const [os, arch] = process.argv.slice(1), a = arch === "amd64" ? "x64" : arch;
    const pk = require(process.argv[3]).packages;
    for (const libc of os === "linux" ? ["glibc", "musl"] : ["any"]) {
      const name = `@anthropic-ai/claude-agent-sdk-${os}-${a}${libc === "musl" ? "-musl" : ""}`;
      const e = pk["node_modules/" + name];
      if (!e || !e.integrity || !e.integrity.startsWith("sha512-")) { console.error("no locked " + name); process.exit(1); }
      console.log(libc, e.resolved, Buffer.from(e.integrity.slice(7), "base64").toString("hex"));
    }' "$1" "$2" "$SHIM/package-lock.json"
}

for p in $PLATFORMS; do
  os=${p%/*}; arch=${p#*/}
  stage="$WORK/stage-$os-$arch"
  mkdir -p "$stage/bin" "$stage/claude-code" "$stage/codex"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X appfactory/internal/node.Version=$VERSION" -o "$stage/bin/factory-node" ./cmd/factory-node)
  ln -s factory-node "$stage/bin/factory-shim"
  ln -s factory-node "$stage/bin/factory-mcp"
  cp "$ROOT/internal/driver/codex/codex.lock" "$stage/codex/codex.lock"
  cp "$WORK/sea/$os-$arch/factory-claude" "$stage/bin/factory-claude"
  claude_lock "$os" "$arch" > "$stage/claude-code/claude.lock"
  echo "$VERSION" > "$stage/VERSION"
  # SBOM (connector-trust 2.2): the Go modules of each binary and the shim's npm lockfile pins, in one JSON file.
  {
    printf '{"version":"%s","platform":"%s/%s","go_modules":' "$VERSION" "$os" "$arch"
    (cd "$ROOT" && go version -m "$stage/bin/factory-node" | awk '$1=="dep"||$1=="mod"{printf "%s{\"path\":\"%s\",\"version\":\"%s\"}", (n++?",":"["), $2, $3} END{if(!n)printf "["; printf "]"}')
    # factory-claude: the Node.js runtime it is built on and the npm packages bundled into it.
    printf ',"node_runtime":"%s","npm_packages":%s,"claude_code":"claude-code/claude.lock","codex":"codex/codex.lock"}\n' "${NODE_SEA_VERSION:-22.18.0}" \
      "$(node -e 'const p=require(process.argv[1]).packages;console.log(JSON.stringify(Object.entries(p).filter(([k,v])=>k&&!v.dev&&!/claude-agent-sdk-/.test(k)).map(([k,v])=>({name:k.replace(/^.*node_modules\//,""),version:v.version}))))' "$SHIM/package-lock.json")"
  } > "$stage/SBOM.json"
  tar -czf "$OUT/factory-node-$os-$arch.tar.gz" -C "$stage" VERSION SBOM.json bin claude-code codex
done

cd "$OUT"
if command -v sha256sum >/dev/null 2>&1; then sha256sum factory-node-*.tar.gz > SHA256SUMS; else shasum -a 256 factory-node-*.tar.gz > SHA256SUMS; fi
echo "$VERSION" > VERSION
ls -la "$OUT"
