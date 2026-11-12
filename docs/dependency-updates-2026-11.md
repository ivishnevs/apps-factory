# Dependency update pass, 2026-11

Task 3.3 of `saas-release-readiness`, done 2026-11-12. Scope: security fixes plus patch and minor updates of the Go
modules, the web (`web/`) and Claude worker (`workers/claude/`) npm packages and the Dex image. Major versions were
taken only where trivial; the others are listed under "Deferred". Pinned runtimes were left alone on purpose:
the Claude Agent SDK / Claude Code version (`@anthropic-ai/claude-agent-sdk` 0.3.284 in `workers/claude`), the
Codex lock (`internal/driver/codex/codex.lock`) and the Node.js version the Claude single executable is built on
(`NODE_SEA_VERSION` 22.18.0 in `scripts/build-claude-sea.sh`, see "Needs a decision").

## Scans before and after

| Scan | Before | After |
| --- | --- | --- |
| `govulncheck ./...` (`go run golang.org/x/vuln/cmd/govulncheck@latest`) | 61 reachable vulnerabilities in 6 modules and the standard library (Go 1.26.0), plus 15 in imported and 19 in required packages that the code does not call | No vulnerabilities found (also with `-show verbose`) |
| `npm audit --omit=dev` in `web/` | 4: next (critical, 36 advisories incl. RCE, SSRF, middleware bypass, cache poisoning), postcss (high, nested in next), sharp (high, libvips/libheif/librsvg), yaml (moderate) | 0 |
| `npm audit --omit=dev` in `workers/claude/` | 3: body-parser (high), fast-uri (moderate), qs (moderate); all under `@modelcontextprotocol/sdk` of the Claude Agent SDK | 0 |
| `npm audit` (with dev) in `web/` | not recorded separately | 1 moderate, dev only: minimatch 10.x under `@typescript-eslint/*` (see Deferred) |
| `npm audit` (with dev) in `workers/claude/` | (the three above) | 0 |

Reachable Go findings before, by source: the standard library (28, fixed by Go 1.26.1 to 1.26.11), go-git
(10, incl. path traversal via reference names, symlink-following worktree operations, credential leak on cross-host
redirect), golang.org/x/crypto (10, SSH DoS and auth issues), golang.org/x/net (7, HTTP/2 server DoS), pgx
(GO-2026-5004, SQL injection via placeholder confusion with dollar-quoted strings), go-billy (2), x/text (2), circl (1).

## What changed

Go (`go.mod`, `go.sum`):

- `go 1.26.0` to `go 1.26.11`: the minimum toolchain now carries the standard library fixes (lipgloss 2.1.0 alone
  needs 1.26.7). Machines on an older 1.26 download 1.26.11 automatically (`GOTOOLCHAIN=auto`).
- `Dockerfile`: `golang:1.26.0-bookworm` to `golang:1.26.11-bookworm` (both stages).
- Direct: bubbletea 2.0.9 to 2.1.1, lipgloss 2.0.6 to 2.1.0, x/ansi 0.11.8 to 0.11.9, go-oidc 3.15.0 to 3.21.0,
  go-git 5.16.2 to 5.19.3, go-jose 4.1.8 to 4.1.9, pgx 5.7.5 to 5.13.0, x/oauth2 0.30.0 to 0.38.0, x/sys 0.47.0 to
  0.50.0.
- Indirect: x/crypto 0.37.0 to 0.59.0, x/net 0.39.0 to 0.62.0, x/text 0.24.0 to 0.44.0, x/sync 0.22.0 to 0.25.0,
  go-billy 5.6.2 to 5.9.2, circl 1.6.1 to 1.6.5, ProtonMail/go-crypto 1.1.6 to 1.5.2, filepath-securejoin 0.4.1 to
  0.7.0, sha1cd 0.3.2 to 0.7.0, ssh_config 1.2.0 to 1.6.0, knownhosts 1.3.1 to 1.3.3, xo/terminfo to 1.3.1,
  go-runewidth 0.0.27 to 0.0.31, go-diff 1.4.0, mergo 1.0.2, go-winio 0.6.4, puddle 2.2.3, ultraviolet (pseudo-version
  of 2026-10-23). No code changes were needed.

Web (`web/package.json`, `web/package-lock.json`):

- next and eslint-config-next 15.3.3 to 15.5.29 (latest 15.x; fixes all next advisories that have a 15.x fix).
- next pins postcss 8.4.31, which is vulnerable; an npm `overrides` entry (`next` > `postcss` `^8.5.32`) moves it to
  the same postcss 8.5.32 Tailwind already uses. Remove the override once next ships a fixed postcss.
- sharp (optional dependency of next) is now 0.35.5; yaml 2.8.1 to 2.9.1 (floor raised to `^2.9.1`);
  @tanstack/react-query 5.107.1, lucide-react 1.72.0, cn 0.5.2, tailwindcss and @tailwindcss/postcss 4.3.5, plus
  in-range transitive updates and `npm audit fix` for dev tooling.

Claude worker (`workers/claude/package.json`, `package-lock.json`):

- `npm audit fix` inside the SDK's tree: body-parser 2.3.0 to 2.4.0, fast-uri 3.1.8 to 3.1.9, qs 6.16.0 to 6.17.1.
  The SDK version itself is unchanged.
- Dev: @types/node 22.18.0 to 22.20.5, esbuild 0.25.10 to 0.25.12 (exact pins kept).

Dex (`compose.yaml`, `deploy/dex/Dockerfile`): `ghcr.io/dexidp/dex` v2.43.1 to v2.46.1. Notes from the release notes
that touch us: config files are now checked strictly (unknown fields refused), gomplate is v5, the
`ContinueOnConnectorFailure` flag is on by default and auth sessions are new. Verified with Docker against the local
test Postgres: the image builds (`apk add postgresql17-client` still works, the image is still Alpine), our
`config.yaml` renders and loads with the Google, GitHub and mock connectors, `ensure-db` creates the database, a
database first created by v2.43.1 starts under v2.46.1, `DEX_STORAGE=memory` starts, and the mock sign-in with
`connector_id=mock` returns a code that the token endpoint exchanges for an ID token with the expected claims.

## Checks after the update

- `make check` with `FACTORY_TEST_DATABASE_URL` (gofmt, `go test -race ./...` incl. storage on Postgres, go vet,
  workers/claude check and test): pass.
- `make build`: pass.
- web: `tsc --noEmit`, `npm run lint`, `npm run build`: pass.
- workers/claude: `npm run build && npm test`: 34 of 34 pass.
- `scripts/build-claude-sea.sh OUT darwin/arm64` with esbuild 0.25.12: builds and the binary starts.

## Deferred (major versions)

| Package | Now | Latest | Why deferred |
| --- | --- | --- | --- |
| next, eslint-config-next | 15.5.29 | 16.4.6 | Major: async request APIs, `next lint` removed (lint must move to the ESLint CLI), caching changes. Not trivial; 15.5.29 has no open production advisories. Plan with the web owner. |
| recharts | 2.15.4 | 3.10.1 | Major API changes in chart components; no advisories against 2.x. |
| typescript (web, worker) | 5.9.3 | 7.0.2 | Two majors (native compiler); 5.9.3 is the latest 5.x. |
| @types/node (web) | 20.x | 26.x | Should follow the Node.js the web image runs (22), as its own small change. |
| @types/node (worker) | 22.20.5 | 26.x | Matches the Node 22 runtime of the shim. |
| esbuild (worker) | 0.25.12 | 0.28.2 | 0.x minors are breaking by semver; the SEA bundle works on 0.25.x. |
| minimatch under @typescript-eslint (web, dev only) | 10.2.6 | 11.x | Moderate DoS advisory (GHSA-g3hv-75wg-m249) fixed only in 11.x, while the latest @typescript-eslint 8.72.2 requires `^10.2.2`. Lint-time only, not shipped. Revisit when typescript-eslint moves. |
| Go: charm.land/bubbletea and lipgloss v1 lines, x/exp etc. | | | Not used or test-only indirect modules; nothing pending among direct Go modules. |

## Needs a decision

- **Node.js for the Claude single executable** (`NODE_SEA_VERSION` 22.18.0, `scripts/build-claude-sea.sh`): Node.js
  has published six 22.x security releases since (22.22.0 on 2026-01-12, 22.22.2, 22.23.0, 22.23.2, 22.23.4 and
  22.23.5 on 2026-11-11). The executable runs the Claude shim on every node, so it should move to 22.23.5. Left
  unchanged in this pass as instructed (it is a pinned runtime that ships to nodes through self-update); recommended
  as a separate change with a node package rebuild and a self-update test.
- **Claude Agent SDK** 0.3.284 (latest 0.3.322): this is the pinned Claude Code runtime, not bumped here.
- `web/Dockerfile` and the root `Dockerfile` use the floating `node:22-bookworm-slim` tag, so they pick up Node 22
  security releases on each image build.
