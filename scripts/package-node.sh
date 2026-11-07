#!/bin/sh
# Builds the node packages handed out by Core (node-install D1):
#   factory-node-<os>-<arch>.tar.gz with bin/{factory-node,factory-shim,factory-mcp,factory-claude}, runtimes/claude.lock
#   (the pinned Claude Code binary install.sh downloads and verifies) and VERSION, for darwin/linux x arm64/amd64, plus
#   SHA256SUMS. factory-claude is the Claude shim as one executable (node-packaging-updates 2.1): no Node.js on nodes.
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
  mkdir -p "$stage/bin" "$stage/runtimes"
  for cmd in factory-node factory-shim factory-mcp; do
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$stage/bin/$cmd" "./cmd/$cmd")
  done
  cp "$WORK/sea/$os-$arch/factory-claude" "$stage/bin/factory-claude"
  claude_lock "$os" "$arch" > "$stage/runtimes/claude.lock"
  echo "$VERSION" > "$stage/VERSION"
  # SBOM (connector-trust 2.2): the Go modules of each binary and the shim's npm lockfile pins, in one JSON file.
  {
    printf '{"version":"%s","platform":"%s/%s","go_modules":' "$VERSION" "$os" "$arch"
    (cd "$ROOT" && go version -m "$stage/bin/factory-node" | awk '$1=="dep"||$1=="mod"{printf "%s{\"path\":\"%s\",\"version\":\"%s\"}", (n++?",":"["), $2, $3} END{if(!n)printf "["; printf "]"}')
    # factory-claude: the Node.js runtime it is built on and the npm packages bundled into it.
    printf ',"node_runtime":"%s","npm_packages":%s,"claude_code":"runtimes/claude.lock"}\n' "${NODE_SEA_VERSION:-22.18.0}" \
      "$(node -e 'const p=require(process.argv[1]).packages;console.log(JSON.stringify(Object.entries(p).filter(([k,v])=>k&&!v.dev&&!/claude-agent-sdk-/.test(k)).map(([k,v])=>({name:k.replace(/^.*node_modules\//,""),version:v.version}))))' "$SHIM/package-lock.json")"
  } > "$stage/SBOM.json"
  tar -czf "$OUT/factory-node-$os-$arch.tar.gz" -C "$stage" VERSION SBOM.json bin runtimes
done

cd "$OUT"
if command -v sha256sum >/dev/null 2>&1; then sha256sum factory-node-*.tar.gz > SHA256SUMS; else shasum -a 256 factory-node-*.tar.gz > SHA256SUMS; fi
echo "$VERSION" > VERSION
ls -la "$OUT"
