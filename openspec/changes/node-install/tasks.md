## 1. Package and downloads

- [ ] 1.1 `scripts/package-node.sh`: packages for darwin/linux × arm64/amd64 with `SHA256SUMS`; Dockerfile builds
  them into the Core image (`/app/dist/node`).
- [ ] 1.2 Core: `GET /install.sh` (rendered with Core's node address) and `GET /downloads/node/<file>`, no credential;
  tests.

## 2. Connection requests

- [ ] 2.1 Storage: migration `0017_node_connects`; create, read, approve (keys active in the company, feed), reject,
  expiry; tests.
- [ ] 2.2 Core: `POST /node-connects`, signed `GET /node-connects/{id}/status`, admin `GET /node-connects/{id}`,
  `POST /node-connects/{id}/approve|reject`; tests.
- [ ] 2.3 Node: `factory-node login` (keys, request, browser, poll, company); tests against a fake Core.

## 3. Installer and web

- [ ] 3.1 `install.sh` (D4): platform, prerequisites, download and sum, unpack, `npm ci`, `node.yaml`, connect,
  service; checked on this Mac against the local Core.
- [ ] 3.2 Owner CLI: `factory nodes install`, `nodes connection ID`, `nodes connect ID [--reject]`. The web pages are
  left to the SaaS redesign of the web (D6).

## 4. Docs and checks

- [ ] 4.1 `docs/node-install.md`; architecture, development and owner-ui updated.
- [ ] 4.2 `make check`, web checks.
