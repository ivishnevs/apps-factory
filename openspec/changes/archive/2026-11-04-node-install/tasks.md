## 1. Package and downloads

- [x] 1.1 `scripts/package-node.sh`: packages for darwin/linux × arm64/amd64 with `SHA256SUMS`; Dockerfile builds
  them into the Core image (`/app/dist/node`). Done: `scripts/package-node.sh` (~11 MB per package, the shim built clean), Dockerfile stage `nodepkg`.
- [x] 1.2 Core: `GET /install.sh` (rendered with Core's node address) and `GET /downloads/node/<file>`, no credential;
  tests. Done: `internal/core/node_connect.go`, `install.sh` embedded; `TestNodeDownloads`.

## 2. Connection requests

- [x] 2.1 Storage: migration `0017_node_connects`; create, read, approve (keys active in the company, feed), reject,
  expiry; tests. Done: `internal/storage/node_connects.go`, migration 0017.
- [x] 2.2 Core: `POST /node-connects`, signed `GET /node-connects/{id}/status`, admin `GET /node-connects/{id}`,
  `POST /node-connects/{id}/approve|reject`; tests. Done: `TestNodeConnect`, admin routes in `TestNodeKeyRoutesNeedAdmin`.
- [x] 2.3 Node: `factory-node login` (keys, request, browser, poll, company); tests against a fake Core. Done: `internal/node/connect.go`, `TestConnect`.

## 3. Installer and web

- [x] 3.1 `install.sh` (D4): platform, prerequisites, download and sum, unpack, `npm ci`, `node.yaml`, connect,
  service; checked on this Mac against the local Core. Done; also installs the supported Codex next to the node and runs doctor. Checked on this Mac against the local Core (download, sum, npm ci, Codex 0.162.1, node.yaml, login approved with `factory nodes connect`, doctor 19/19, the node registered with codex and claude; a second run upgraded in place without a new connection; the test node was revoked) and on Linux arm64 in `node:22-bookworm` up to the connection (refused there by design: http outside localhost). `plutil -lint` on the launchd plist. Found and fixed: `factory-node doctor` probed `CODEX_BIN`/PATH instead of node.yaml's drivers.
- [x] 3.2 Owner CLI: `factory nodes install`, `nodes connection ID`, `nodes connect ID [--reject]`. The web pages are
  left to the SaaS redesign of the web (D6). Done: `cmd/factory/nodes.go`.

## 4. Docs and checks

- [x] 4.1 `docs/node-install.md`; architecture, development and owner-ui updated. Done: `docs/node-install.md`, development and architecture.
- [x] 4.2 `make check`, web checks. Done: `make check` green. No web changes (D6).