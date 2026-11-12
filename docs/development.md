# Development

Go 1.26 (`crypto/hpke` seals company secrets to nodes, core-secret-store D4; Charm v2 libraries need 1.25); pgx 5.7.5 (`pgx/v5/stdlib` behind
`database/sql`), yaml.v3 3.0.1. pgx is pure Go; no CGO toolchain is needed in the Core container.

Storage is Postgres only (postgres-tenancy). `FACTORY_DATABASE_URL` (`postgres://user:password@host:5432/factory`)
names Core's database; Core keeps no database file. Queries are written with `?` placeholders: `storage.DB`
and `storage.Tx` number them (`$1`, `$2`, ...) and pass Go booleans as the 0/1 integers of the flag columns.
Entity documents (`body`, `spec`) are `json` (kept byte for byte as written; `->>` reads fields), times Unix
seconds (`bigint`), and every table that is read in
insertion order has a `seq` identity column. An integrity violation (SQLSTATE class 23: unique, foreign key,
check) maps to `ErrConflict` (409), as before.

Migrations: `internal/storage/migrations/NNNN_name.sql`, embedded, applied in order, each in its own transaction,
recorded in `schema_migrations(version, name, applied_at)`. Core takes a session advisory lock while migrating,
so Core processes starting together apply each migration once; Core refuses to start on a database whose highest
version is newer than it knows. `0001_baseline.sql` is the whole current schema (no database was kept from the
SQLite era: nothing is imported); add a change as the next numbered file (`0002_...`).

Tenancy: accounts own companies and users; `memberships` give a user a role (`owner`, `admin`, `member`) in companies of
its own account (the composite foreign keys refuse anything else). Every domain table has `company_id`
(referencing `companies`), its key is `(company_id, id)` and every foreign key includes `company_id`, so a row
can never reference another company's row (the database refuses the write; Core answers 409 without partial
effects). Row IDs also stay globally unique, so a lookup by a Core-issued ID cannot reach another company.
Unique partial indexes (one live run per task, session, workspace writer and agent; open questions, parks,
alerts, escalations) start with `company_id`. Every `Store` method takes the company right after `ctx`, and
transactions carry it in `storage.Tx` so each insert and each lookup by a caller-supplied ID is scoped. Core
serves one company (`FACTORY_COMPANY`, `Core.Company`, default `default`); `companyFrom(r)` resolves it per
request, and run credentials bind the company into their HMAC, so a token for another company's run is rejected.

Locking: every Store transaction (`Store.begin`) takes `pg_advisory_xact_lock(<class>, hashtext(company))` first.
Transactions that read state and decide on it (dispatch, claim, lease expiry, approval expiry, message scheduling,
every mutation) therefore run one at a time per company, as they did on the single SQLite connection, while
different companies never wait for each other; plain reads take no lock. Unique partial indexes remain the last
line of defence. A transaction that queues a run or changes what a node must do (`Tx.wakeNodes`) wakes the company's
waiting node polls after its commit (`Store.Wake`); the polls re-read every 2 s, so another Core process's commits
reach them too.

Catalog per company: `configs/` is the `default` company's catalog; another company's lives in
`configs/<company>/` with the same layout (`teams/`, `agents/`, `profiles/`, `policies/`, `node-hooks/`, `quota_keys.yaml`,
`company.yaml`, `skills.yaml` — the company's installed skills (by source and hash, contents in Core), no fallback to `configs/skills.yaml`; see
[skills](skills.md)). `configs/prices.yaml` is shared, prompt files are paths relative to the root. `factory config validate --company X` (or `FACTORY_COMPANY=X`) checks one company's catalog; agents of
the same name in two companies are independent.

These directories are seed data: Core stores each company's catalog as versions ([stored catalogs](catalog.md)).
When Core starts and its company (`FACTORY_COMPANY`) has no stored version, the repository's directory becomes
version 1; after that **editing `configs/` changes nothing in a running Core**. Change the catalog with
`factory catalog pull DIR` / `push DIR` (or the web UI's Team page); to start over from the repository on a
throwaway database, drop the company's `catalog_versions`/`catalog_documents` rows and restart Core. Starter teams for
`factory companies create` are the team presets in `presets/teams/` ([catalog](catalog.md#presets-and-the-team-builder)).

Nodes and Core: there is no broker. A node signs every request with its own key ([Node identity](#node-identity)),
long-polls `POST /nodes/{node_id}/poll` for offers of queued runs and stops, claims an offer, and sends run events
to `POST /runs/{run}/events` from its spool ([runtime protocol](runtime-protocol-v2.md) §4, §8). The node's key
fixes its company; node routes refuse another company's or another node's runs.

Backups: back up the Postgres database (`pg_dump`, below), the blob store (`FACTORY_BLOB_DIR`, default
`<data dir>/blobs`, the `core-blobs` volume: artifact contents, attachments and run logs by
digest; back it up after the database, a blob newer than the dump is harmless); Core's data
directory (`FACTORY_DATA_DIR`, default `data`, the `core-data` volume) holds only `core.incarnation`. Core mounts
no project workspace. Back up before upgrading; a downgrade requires restoring a compatible backup (an older Core refuses a newer schema).

```sh
docker compose exec -T postgres pg_dump -U factory -Fc factory >backup/factory.dump   # online, consistent
```

`make build`, `make check`, `make up`, `make down`, `make schema`, `make test-services`, `make test-services-down`.
`make hooks` (once per clone) enables `.githooks/pre-commit`, which refuses a commit whose staged Go files are not
gofmt-formatted; `make check` also runs `make fmt` (gofmt over `cmd` and `internal`).
FACTORY_ROOT selects config root; FACTORY_DATABASE_URL the database; FACTORY_COMPANY the company Core serves;
FACTORY_DATA_DIR Core's data directory; FACTORY_BLOB_DIR Core's blob store; FACTORY_LISTEN selects HTTP bind (default: `0.0.0.0:$PORT` when the host sets PORT, as Railway does, else 127.0.0.1:8080).
FACTORY_URL is the native CLI/node endpoint.
CODEX_BIN selects the installed native Codex executable. FACTORY_TOKEN is required by Core and the owner CLI; a node
does not use it (see [Node identity](#node-identity)).
Core uses signal cancellation, HTTP shutdown deadlines and structured JSON logs.
The node handles SIGTERM/SIGINT by requesting turn interruption before closing its process group.

Tests: every test that touches storage needs Postgres. `make test-services` starts it in a tmpfs container
(127.0.0.1:15432); the Makefile exports
`FACTORY_TEST_DATABASE_URL=postgres://postgres:factory@127.0.0.1:15432/postgres?sslmode=disable` (export it yourself
for a plain `go test`). Without it those tests are skipped with a message. No broker is needed: node and executor
tests run a real Core handler over `httptest` and packages run in parallel. Each test gets its own database cloned from a template database built once per schema
(`ft_<schema hash>`, kept on the server; `internal/storage/pgtest`, `storagetest.Open(t)`), and dropped at the
end. Use a server you can throw away: tests create and drop databases on it. Full suite:
`go test -race -count=1 ./...` (or `make check`).

Normal tests use fake RPC subprocesses or the internal/driver/fake scripted driver, with no model/network.
scripts/smoke.py is a separately invoked
real ChatGPT smoke test and consumes subscription usage. It verifies start, streaming, completion
and thread resume, and prints only run metadata and requested final text.

Logs: `docker compose logs core`, `docker compose logs postgres`; the node logs to stderr.
Do not print .env or database URLs with passwords in reports. Back up Postgres and the blob volume.
`docker compose down -v` deletes durable state and is not the normal stop command.

Foundation limitations: no remote machine path mapping (one canonical node path per project), no event
retention policy. Factory MCP enforces run-scoped Core access. Skill operations enforce the shared policy evaluator; shell access is trusted.
Do not proceed to later stages until Foundation checks pass.

Task Engine adds scripts/task-smoke.py (opt-in real model chain; --restart-core tests persistence),
migration/DAG/version/idempotency/review/retry tests and a fake-runtime restart test.
See task-lifecycle.md for the supported API/CLI and retry constraints.

Factory MCP builds a native sibling factory-mcp executable. See factory-mcp.md for scoped auth,
stdio tools, audit and the opt-in scripts/mcp-smoke.py planner/executor/reviewer test.

Messaging adds scripts/messaging-smoke.py --restart-core, blocking-answer races, communication scope,
form/duplicate/deadline/escalation/session tests. See messaging.md for HTTP/CLI and MCP contracts.

Action approval verification: `make check` covers exact grants, persistence, denial, expiry, revocation and writer quarantine. `scripts/approvals-smoke.py --restart-core` uses real Codex for approval and rejection with task/thread continuation. See [approvals](approvals.md).

Versioned artifact verification: `scripts/artifact-approvals-smoke.py --restart-core` exercises real Codex registration/submission, independent task review, v1 approval, v2 rejection and a v3 revision unblocking QA. See [artifact approvals](artifact-approvals.md).

Reports and decisions: `scripts/journal-smoke.py --restart-core` verifies three real Codex tasks, pending proposal persistence, independent inbox/task review, explicit replacement and captured run contexts. See [reports and decisions](reports-decisions.md).

OpenSpec lives in each project's repository (openspec-in-git; migration `0006_openspec_in_git` dropped `spec_documents`): agents commit specs with their code, the node checks a linked change on the checkout, and `done` facts report the change's state. `scripts/openspec-smoke.py` runs the proposal -> implementation -> archive flow on a local repository plus a `change_missing` check. See [OpenSpec](openspec.md).

### Live smokes (cheap)

Live smokes spend subscription usage; run them against the **smoke company** (`configs/smoke/`), never the
production catalog. It has only the agents the smokes use, on the cheapest models at `effort: low`, with a 4 min run
timeout and no retries: `demo` and `manager` on Codex `gpt-6-luna` (the cheapest entry of the Codex model catalog,
`~/.codex/models_cache.json`), `claude-demo` on Claude `claude-haiku-5-5` with `max_turns: 10`
(`TestSmokeCatalog` pins this). If a step fails because the model is too weak, raise that one agent (e.g.
`gpt-6.1-sol`) rather than the whole catalog. All three live smokes run with explicit exits on
(`FACTORY_EXPLICIT_EXITS=1`, the default) against one Core: every agent run ends with a `factory_exit_*` call and
the scripts check each run's `ENDED`/`succeeded` outcome and accepted exit. `messaging-smoke.py`,
`openspec-smoke.py` and `drivers-smoke.py` check `GET /health` first (`company`, `explicit_exits: on`) and refuse to
start a model run against the wrong Core (`SMOKE_COMPANY=any` skips the company check); each stops at its first
failed step.

```sh
# .env: FACTORY_TOKEN, POSTGRES_PASSWORD as usual. One Core for the smoke company, exits on:
FACTORY_COMPANY=smoke FACTORY_EXPLICIT_EXITS=1 docker compose up -d --build core
./bin/factory config validate --company smoke
cat > data/node-smoke.yaml <<'YAML'
state_dir: data/factory-node-smoke
drivers:
  codex: {enabled: true}
  claude: {enabled: true}
YAML
# Node environment as usual (FACTORY_URL); once per state_dir, enroll it with this Core (Node identity):
./bin/factory nodes enroll --name smoke
./bin/factory-node enroll -config data/node-smoke.yaml CODE     # then factory nodes approve NODE_ID FINGERPRINT
# Claude on the personal subscription (claude-runtime.md, Personal subscription):
CLAUDE_CONFIG_DIR=$HOME/.claude-personal ./bin/factory-node doctor -config data/node-smoke.yaml
CLAUDE_CONFIG_DIR=$HOME/.claude-personal ./bin/factory-node run -config data/node-smoke.yaml

# then, in order (each script makes its own project):
# hierarchy 3.2 (Codex only, 7 runs: owner 2 demo; agent 2 demo + 1 manager; dialog 2 manager):
scripts/messaging-smoke.py --restart-core
# openspec-in-git 6.1 (Codex only; plan, implement, usually 1 short manager conversation run for delegated_closed):
scripts/openspec-smoke.py
# runtime-drivers 4.2 (Claude developer + Codex reviewer; 3 runs: Claude submit, Codex approve, Claude done):
scripts/drivers-smoke.py
```

`messaging-smoke.py --only agent,owner,dialog` (comma list) runs a subset; `--restart-core` applies to `owner`
(the owner question waits in the inbox across `docker compose restart core`). The exits used: messaging `agent`
asks the manager with `factory_exit_ask` (the manager's conversation task answers with `factory_message_reply` and
`factory_exit_done`), then submits; `owner` asks the owner (`factory_exit_ask`, locked category `release`), then
submits; the owner accepts both; `dialog` runs end with `factory_exit_done` and the conversation task stays open.
Overlap: `drivers-smoke.py` repeats the explicit exits of `exits-smoke.py` across two
runtimes, so a passing `drivers-smoke.py` makes a separate `exits-smoke.py` run unnecessary. About 13 cheap runs in
all (messaging 7, OpenSpec 2–3, drivers 3). Results land in `.cache/messaging-verification.json`,
`.cache/openspec-verification.json` and `.cache/drivers-verification.json` (run ids, sessions, exits,
`GET /tasks/{id}/costs`).

## Secrets

Company secrets are encrypted in Core's database (core-secret-store): a master key wraps one key per company, and
each value is AES-256-GCM under its company key. The master key is Core's `FACTORY_SECRETS_KEY`, the base64 of 32
random bytes:

```sh
openssl rand -base64 32                      # once; put it in .env as FACTORY_SECRETS_KEY=...
gopass insert factory/secrets-master-key     # back it up outside this machine: without it every secret is lost
```

Core refuses to start with a key that does not open the existing company keys (or without one once a company has
secrets); without any key it runs, but secrets can be neither set nor delivered (claims of runs that need one stay
QUEUED). Rotation: set the new key as `FACTORY_SECRETS_KEY_NEW` next to the old one, restart Core, run
`factory secrets rotate-master` (operator token; re-wraps every company key under the new key in one transaction),
then make the new key `FACTORY_SECRETS_KEY`, drop `FACTORY_SECRETS_KEY_NEW`, restart Core and update the backup.
Nodes keep nothing: each claim answer carries the run's values sealed to the node's encryption key
([runtime protocol](runtime-protocol-v2.md#5-claim-phase-1)); file secrets live under
`<state_dir>/runs/<run>/secrets/` (0600) for the run only and leftovers are removed at node start.

## Sign-in

The website signs people in through Dex (user-accounts D3): `compose.yaml` runs `dex` (`deploy/dex/config.yaml`,
rendered by the Dex image with gomplate) on `127.0.0.1:5556` with its own `dex` database in the same Postgres
(`dex-db` creates it once). Core is the only OIDC client (`factory-core`); browsers reach Dex at the issuer, Core
at `http://dex:5556/dex` (`FACTORY_OIDC_INTERNAL_URL`). In `.env`:

```sh
FACTORY_OIDC_ISSUER=http://127.0.0.1:5556/dex   # turns sign-in on (empty: tokens only)
FACTORY_OIDC_CLIENT_SECRET=<openssl rand -hex 32>   # shared by Dex and Core
FACTORY_PUBLIC_URL=http://127.0.0.1:3000        # the web UI; https turns on Secure cookies
FACTORY_OIDC_PROVIDERS=mock                     # buttons: Dex connector ids (google,github in production)
DEX_MOCK_CONNECTOR=1                            # local development: "Continue as the test user"
```

Google: an OAuth client (Web application) with the redirect URI `<issuer>/callback`, then `GOOGLE_CLIENT_ID` and
`GOOGLE_CLIENT_SECRET`. GitHub: an OAuth App with the callback URL `<issuer>/callback`, then `GITHUB_CLIENT_ID` and
`GITHUB_CLIENT_SECRET`. A connector is configured only when its client id is set. Then `make up` and `npm run dev`
in `web/`, and open the web UI one of two ways:

- `http://127.0.0.1:3000` — Next.js sends `/api` and `/auth` to Core (`FACTORY_URL`, `next.config.ts` rewrites).
  Quickest for UI work; `FACTORY_PUBLIC_URL=http://127.0.0.1:3000`.
- `http://127.0.0.1:8080` — as in production (`docs/deploy-railway.md`): Core serves `/api` and `/auth` and proxies
  the pages to Next.js. In `.env` set `FACTORY_WEB_UPSTREAM=http://host.docker.internal:3000` (Core in compose) or
  `http://127.0.0.1:3000` (Core on the host) and `FACTORY_PUBLIC_URL=http://127.0.0.1:8080`, then `make up`. Without sign-in, set
`FACTORY_WEB_LOCAL=1` for Core to serve the web API as the owner on a loopback Core. Core keeps no provider tokens; sessions are rows of
`sessions` (only the SHA-256 of the cookie's id), 30 days sliding, 90 days at most. `FACTORY_MULTI_COMPANY=1` lets
people create more companies; `FACTORY_SIGNUP_TEMPLATE` (default `solo`) is a new company's starter catalog. Dex's
state (signing keys, refresh tokens) is in the `dex` database: back it up with the `factory` one.

Tests: `internal/auth/authtest` is a fake issuer (PKCE, nonce, RS256); `TestDexMockConnector` signs in through a
real Dex when `DEX_TEST_ISSUER` (and `DEX_TEST_SECRET`) name one started from `deploy/dex/config.yaml` with
`DEX_MOCK_CONNECTOR=1`.

## Node doctor

`./bin/factory-node doctor` runs the node self-checks locally (node-agent D7); Core is checked only when
`FACTORY_URL` is set. It prints a
STATUS/CHECK/DETAIL table with a remediation hint under every `fail`/`warn` row and exits 1 when any check fails.
Checks: `git` and `git.user`; `forge.auth` (`gh auth status`, warn only) and, with `-repo URL`, `forge.repo`
(`git ls-remote`); `disk` free space at `-disk-path` (default `$HOME`) against `-low-gib 20` (warn) and
`-critical-gib 5` (fail); on macOS `xcode` (`xcode-select -p`, `xcodebuild -version`) and `xcode.simctl` (available
simulators; warn when none); `factory-mcp` and `factory-shim` in `FACTORY_BIN_DIR`, next to the binary, or `./bin`;
`ps` (`ps -o lstart= -p <pid>` must print a start time: runtime process groups are recorded by their leader's start
time so a stop never signals a reused PID; without it a stop kills only the runtime's leader and can orphan its
children, and the node logs that once; hint: install procps).
`node.keys`: the keys in `state_dir/keys` and their fingerprint (warn when the node has not enrolled, fail when a key
file is invalid or readable by group or others).
`core` (with `FACTORY_URL`): a `GET /health` signed with the node's keys (fails when the keys are missing, the
signature is refused, Core is unreachable, or Core answers 503 because it cannot reach its database; the
clock offset from Core's `Date` header warns above 60 s and fails above 120 s). It is read-only, times out after
5 s, never prints keys, and is `skip` without `FACTORY_URL`.
Runtime probes (`node.RuntimeProbe`, binary/version/auth, never a model call; drivers own them and `cmd/factory-node`
injects them as `Doctor.Runtimes`): `codex.*` (`codex.Driver.Probe`: `CODEX_BIN`, exactly
`codex-cli 0.162.1`, `codex login status`) and `claude.*` (what the Go driver runs: Node 22+, the built
`workers/claude/dist/src/shim.js`, and `shim.js --probe` for the Claude Code version and login it will use, without a
model call; see [Claude runtime](claude-runtime.md)). By default only installed drivers are probed;
`-drivers codex,claude` requires the listed ones. `drivers` fails when no driver is usable.
Stack checks: the `checks` of the company catalog's `NodeHooks` document (below) as Core last delivered it to
this node (`<state_dir>/node-hooks.json`, written by `factory-node run`; `-config` gives the state_dir and labels),
each as `stack.<name>` (command exits 0 = pass), `skip` on a node lacking one of the check's labels.

### NodeHooks (company catalog)

`configs/node-hooks/<name>.yaml`, `kind: NodeHooks` (node-agent D4, D7; `factory config validate` loads them):
`spec.on_run_stop`, `spec.on_task_closed`, `spec.prune` (`[{command, older_than: 72h}]`) with the shape of
node.yaml `janitor`, and `spec.checks` (`[{name, command, hint, labels}]`). A node names its document with
node.yaml `hooks` (default `default`). Core delivers it in the register answer and again in a heartbeat answer when
the hash the node applied differs (runtime protocol v2 §3, §7). The node hands the hooks to its janitor (a list
node.yaml `janitor` sets overrides the catalog's: local override and fallback), runs the stack checks with its
self-checks, and keeps a copy in `<state_dir>/node-hooks.json` for the doctor. A name without a document is
delivered `missing` with an empty spec. Hooks and checks are trusted template data run with `/bin/sh -c`.

## Node process

`./bin/factory-node run [-config node.yaml] [-slots N]` (`make node`) is one process with N slot goroutines
(node-agent 1.1; registration and the inventory heartbeat, below). One long poll (`POST /nodes/{node_id}/poll`,
[runtime protocol](runtime-protocol-v2.md) §4) sends the idle slots per usable driver and gets offers of queued runs,
which go to idle slots; each slot runs the existing executor (`worker.Worker.ProcessRun`: claim, execute). The poll
answer also carries Core's urgent stops, worktree resets and cleanups; the heartbeat answer stays the guarantee. A
claim Core refuses with `retry` keeps the run off the node's offers for 5 s. Run events are spooled in
`<state_dir>/runs/<run>/events.jsonl` and sent in order to `POST /runs/{run}/events`; while Core is unreachable runs
go on and the spool waits; a restarted node resends what it finds there. The company is `state_dir/company`, stored
at enrollment; the node refuses to run when the register answer names another company for its key.
Environment: `FACTORY_URL` (default `http://127.0.0.1:8080`; HTTPS unless on this machine), `CODEX_BIN`.
The node has no token and refuses to start before it has enrolled (see Node identity below). At least one
enabled driver must pass its version check and probe at start (see Driver checks below). SIGINT/SIGTERM interrupt running runs and wait
for them.

`node.yaml` comes from `-config`, else `FACTORY_NODE_CONFIG`; without either the defaults apply. Unknown keys are
errors. Relative paths (`state_dir`, `projects_root` (`~/` is the home directory), `bin_dir`, `disk.path`, `worktrees.root`,
`drivers.claude.shim`, and a driver `binary`/`executable` that holds a `/`)
resolve against the working directory the node starts in, not the config file's directory: start it from the
checkout for `state_dir: data/...`. A bare `binary: codex` stays a PATH lookup.

```yaml
name: mini-1                 # default: host name; a label only (Core identifies the node by node_id)
slots: 2                     # 1..64
conversation_slots: 1        # 0..64, on top of slots: conversation runs only (work-and-conversation-runs)
resources: {simulator: 2, build: 2}  # capacity per class, counts >= 0; sent in register, Core enforces at claim
slots_max: 4                 # caps the slots the settings from the web may set (node-install.md); remote: off|{key: false} pins
labels: [macos, xcode]       # sent in register; Core claims an agent's run only where all its spec.labels are
runtime_env: [XCODE_*, MY_TOOL_HOME]  # extra variables agent runtimes inherit (allowlist; credentials never pass)
state_dir: data/factory-node # keys, company, pidfiles, node_id, epoch; never shared by live nodes
projects_root: ~/projects     # repositories of new projects and clones of existing ones by URL (projects.md); default <state_dir>/projects
bin_dir: /opt/factory/bin    # factory-mcp, factory-shim and ../workers/claude/dist/src/shim.js; default: next to the binary
drivers:
  codex: {enabled: true, binary: codex}  # binary default: CODEX_BIN, else codex
  claude: {enabled: false}               # Go driver (internal/driver/claude); keys node, shim, executable, allow_api_key
probes: {interval: 10m, version_interval: 1h, attempts: 3, retry_backoff: 5s, transient_failures: 3}  # driver check periods (at least 1m) and transient-failure handling (defaults)
disk: {path: data/factory-node, low_watermark_gib: 20, critical_watermark_gib: 5, conversation_floor_gib: 1}  # path default: state_dir
worktrees: {enabled: false, root: data/factory-node/worktrees, scan_command: [gitleaks]}  # root default: <state_dir>/worktrees
poll: {wait_seconds: 25}      # how long Core may hold the poll open (1..25, default 25); lower behind a proxy that cuts requests sooner
hooks: default               # NodeHooks document of the company catalog (protocol v2; default: default)
janitor:                     # local override of the NodeHooks lists it sets; each command runs with /bin/sh -c
  interval: 10m              # prune period (default 10m)
  hook_timeout: 5m           # per command (default 5m)
  on_run_stop: ['xcrun simctl delete "af-$FACTORY_TASK_ID" || true']    # FACTORY_RUN_ID, FACTORY_TASK_ID, FACTORY_WORKTREE
  on_task_closed: ['xcrun simctl delete "af-$FACTORY_TASK_ID" || true'] # FACTORY_TASK_ID, FACTORY_WORKTREE
  prune:
    - {command: 'find ~/Library/Developer/Xcode/DerivedData -mindepth 1 -maxdepth 1 -mmin +$FACTORY_OLDER_THAN_MINUTES -exec rm -rf {} +', older_than: 72h}
```

Claude runs only through the node: enable `drivers.claude` on nodes logged in to Claude (it is off by default; see
[Claude runtime](claude-runtime.md)). The standalone TypeScript service and `factory-workspace` were removed
(runtime-drivers 4.1).

- Start: runtime process groups recorded in `state_dir` by a previous process are killed (`worker.KillOrphans`).
- Driver checks (runtime-drivers D4): each enabled driver's version check (`CheckVersion`) runs at start and every
  `probes.version_interval`, its `Probe` (binary, version range, authentication; never a model call) at start and
  every `probes.interval`. A failing check disables that driver on this node: the poll stops asking offers for it
  (one error log), the other drivers keep claiming, running runs continue; the next passing check
  re-enables it (one info log). A transient failure (the probe killed by its timeout, as on a loaded machine,
  no output at all, or a process that could not start for lack of resources; `CheckResult.transient`) is not a
  finding: the check is retried up to `probes.attempts` times in all, `probes.retry_backoff` apart (doubling),
  with the 30 s command timeout doubled each time; a check that still fails only transiently logs a warning and
  leaves the driver as it was, and only `probes.transient_failures` such checks in a row disable it. A run on the
  driver that succeeded or produced runtime events since the previous check is evidence of health: a transient
  failure then never disables the driver. Definitive failures (not logged in, unsupported version, missing
  binary, Node or shim) disable it at once, as before. A run that ends on the runtime's authentication failure (`driver.AuthFailure`:
  Claude `auth_error`, Codex without a ChatGPT login; such runs end `paused_out`) disables its driver the same
  way at once, without waiting for the next probe, for a hold-off of 10 min, then 30 min, then 2 h (cap) for each
  further auth failure; a passing probe re-enables it only after the hold-off (one info log when a probe passes
  during it), so a probe that passes while runs keep failing auth does not flap the driver. Failures of runs that
  were in flight while it is disabled do not escalate; the escalation starts over after a run on that driver
  completes, or after it ran 2 h past its last hold-off without an auth failure (runtime-drivers 3.2).
  With no usable driver the node claims nothing; the node refuses to start when
  none is usable at start. Every inventory heartbeat carries the drivers (`ok`, and for
  a disabled one the failing `check` — `version`, `probe` or `auth` — with its `detail`), and a flip sends a
  heartbeat at once: Core updates the node's drivers without a new epoch, claims follow them, and Core records one
  `driver_unsupported` attention event per driver that becomes unusable (`driver_restored` when it is usable again).
- Disk: before each claim a free slot reads free space on `disk.path`. Below the critical watermark work and
  review claims pause while conversation claims go on (disk-pause-conversations: an answer takes no disk); below
  `disk.conversation_floor_gib` (default 1 GiB, at most the critical watermark) every claim pauses; below the low
  watermark one warning is logged; each crossing logs once and clears with a log line when space returns. Running
  runs continue. With protocol v2 the heartbeat's `health.disk` (with `disk_conversation_floor_bytes`) makes Core
  raise one `node.low_disk` per low episode and one `node.disk_critical` (and answer `pause_work`) per critical one;
  an owner pause of the node answers `pause` and stops every claim.
- Self-checks (node-agent D7, protocol v2): at start, every `probes.interval` and when the `NodeHooks` checks change,
  the node runs `git`, `git.user`, `forge.auth`, `ps` and the stack checks; the failing ones go into the heartbeat's
  `health.checks` (driver probes do not: a failing driver is one `driver_unsupported` through `drivers`), and Core raises one `node.check_failed{check}` per failure (and
  `node.check_passed` when it passes again). A failing `git` also stops claiming until it passes; the other checks
  only report. A heartbeat that has not succeeded for 5 minutes is reported as `node.heartbeat_stalled` with the
  first heartbeat that gets through. Every register says why (`start` or `reregister`; driver changes ride the heartbeat); Core counts process
  starts and raises one `node.restart_loop` at 4 restarts within 15 minutes. `factory status` ends with the nodes
  (online/offline, epoch, held/total slots and resources, drivers, disk, load, failing checks); `factory watch --attention` shows
  the health alerts.
- Janitor (node-agent D4; hooks from the catalog's `NodeHooks` document, node.yaml `janitor` lists override them):
  `on_run_stop` runs after every run (after the seal and the terminal event, before the slot claims again).
  `prune` runs every `interval` with `FACTORY_OLDER_THAN_MINUTES` from `older_than`, and at once with 0 when free
  space on `disk.path` falls below the low watermark (again every interval while it stays low). For each task in
  Core's `cleanup` list (inventory heartbeat) the janitor takes the worktree lock (a running run keeps it; retried
  on the next list), stops a leftover run group, seals, runs `on_task_closed` once, then removes the worktree and
  its scratch directory; the branch and `refs/factory/wip/<task>` stay in the repository. A failed seal keeps the
  worktree. Hook failures and timeouts are logged only.
- Watchdog (every 30 s): a busy slot whose recorded runtime group is gone has its run cancelled (the executor reports
  it); a recorded group without a busy slot is killed and its pidfile removed; an idle slot whose claim loop has not
  turned for 2 minutes is restarted. Busy slots are bounded by the run timeout, not by this check.

- Skills (skill-registry D8): a run's offered skills are downloaded from Core by the hash pinned on the run into the
  node's store `<state_dir>/skills/store/sha256-…` (verified with `skills.Hash`, read-only, kept while used: entries
  unused for 14 days are removed at node start and daily) and copied into a temporary per-run view outside the
  workspace; a hash that does not verify or Core does not hold ends the run `setup_failed` before the runtime starts.
  `skills_root` is gone: nodes need no copy of the catalog or skill files.
- Sessions (runtime-drivers 2.7, `ResumePolicy`): `<state_dir>/sessions` records each session this node ran
  (session id, task, agent, run, whether the prompt was delivered, terminal status). A run resumes
  `RunSpec.session_id` only when this node recorded it for the same task and agent and its prompt was delivered;
  otherwise (another node, a reset workspace, an unknown session) it starts a fresh session and its prompt begins
  with a note that the task's context comes from Core.
- Command guards: runs whose captured rules deny or require approval for commands (always, through the
  mandatory `host-ui` and `owner-cli` policies) need `factory-shim` (`make build`; `bin_dir`, else next to the
  binary). The executor fetches the run credential, creates a temporary shim directory first on PATH
  (`FACTORY_SHIM_DIR`) and removes it after the run; see [policies](policies.md#command-guards). iOS/Android
  agents get the alternatives to host UI input (debug launch arguments, deep links, UI tests by accessibility
  identifier) from the shared `mobile-app-testing` skill ([skills](skills.md#mobile-app-testing)). Executor tests
  script shell commands with `fake.Activity.Exec` (`internal/worker/shim_test.go`).

### Outbound connection

The node only dials out, so a machine needs no inbound port: every exchange with Core is a signed HTTPS request
to `FACTORY_URL` (plain HTTP only to this machine). A reverse proxy in front of Core must keep a request open at
least `poll.wait_seconds` + a few seconds (25 s by default) or the node's `poll.wait_seconds` must be lowered.

### Explicit exits on the node

With Core's `FACTORY_EXPLICIT_EXITS=1` runs end with `factory_exit_*` ([contract](explicit-exits-contract.md)).
`factory-mcp` then needs `git` (and `gh` for pull requests) on the node; it collects the exit facts in the run's
working directory, refuses a dirty tree or a scan finding locally, pushes the task branch and opens its pull request
before `submit`/`handoff`, and writes the exit state to `FACTORY_EXIT_FILE`. The executor reports it on the terminal
event, stops a runtime that keeps running 30 s after its exit, and turns on the exit guard (Codex follow-up turn,
Claude Stop hook, 2 attempts each).

### Runtime protocol v2

The node speaks [runtime protocol v2](runtime-protocol-v2.md):

- Identity: `<state_dir>/node_id` is a random id generated on first start and never changed (`name` stays a
  label); a damaged file stops the node instead of creating a new identity. `<state_dir>/epoch` is incremented and
  fsynced on every start and on every re-register. Copying a state dir to a second machine makes both one node:
  Core refuses the older epoch (`409 stale_epoch`) and that process exits.
- Start: orphaned runtime groups are killed, worktrees whose run pidfile outlived the previous process are stopped
  and sealed locally (not reported), then `POST /nodes/register` sends `node_id`, `name`, `epoch`,
  `protocol_version`, `slots`, `drivers` (`CheckVersion` version, `ok` when no driver check fails, else `check` and
  `detail`), `resources`, `labels`, `worktrees`, `hooks` and `reason` (`start` or `reregister`). Unreachable Core is retried (up to 30 s apart); a refusal stops the node.
- Commands are pointers: only the run id is read (`run_id`, or `id` of a full RunSpec during the transition). A
  free slot claims with `{node_id, epoch, claim_id, slot}`; `claim_id` is `<epoch>-<slot>-<n>`, an idempotency key
  reused when a transient failure (network, 5xx) is retried. `409 retry` naks the command with a 5 s delay,
  `409 drop` acks it; on `200` the command is acked and the executor runs the claim's `run_spec`. After the
  workspace is allocated and the runtime launched, `POST /runs/{id}/start {lease}` (retried) moves the run
  from CLAIMED to RUNNING; a failed start ends the run `failed`. There are no per-run
  heartbeats.
- Every `heartbeat_seconds` (register answer, default 10): `POST /nodes/{node_id}/heartbeat` with the inventory
  (`claimed`, `starting`, `running`, `ended_unacked` with `elapsed_ms` since the claim on the monotonic clock)
  and health (`load1`, `cpus`, `free_disk_bytes` and `disk` ok/low/critical on `disk.path` by the node.yaml
  watermarks, `busy_slots`) and the current `drivers`. The answer's `acked` drops ended runs, `stop` cancels runs through the executor
  (which interrupts, seals and reports them; a stopped ended run is dropped), `pause` stops claiming on top of the
  local disk gate, `cleanup` task ids have their worktrees sealed and removed (`git worktree remove`; branch, WIP ref
  and clone stay; a worktree in use is retried on a later listing) or are logged when worktrees are off, `reset`
  task ids get `janitor.ResetWorkspace` (confirmed in the next heartbeat's `reset_done`), and `hooks` brings a changed
  `NodeHooks` document (the heartbeat sends `hooks` and the applied `hooks_hash`). A failed heartbeat is logged;
  runs continue.
- A changed `incarnation` (or `404` for the node) stops claiming, stops every run, waits for them to end, then
  registers the next epoch with an empty inventory. `409` on a heartbeat stops the node.
- Capacity (Core): a run holds its slot and resource classes from the claim until it has ended in Core **and** the
  node no longer lists it unended; the node lists a run `ended_unacked` only after its seal and run-stop hooks, so
  an ended but unsealed run still holds its class. A new epoch releases what the old process held. Agents'
  `spec.labels` must all be node labels; a shortfall answers `409 {"retry":true,"reason":"capacity","detail":…}`.
  `factory tasks why` shows `no_capable_node` (no online, unpaused node has the assignee's driver, labels and
  resources; "resume node N" for an owner pause), `node_disk_critical` (the capable nodes are paused for a critical
  disk: "free disk on N; it resumes by itself above 5 GiB"), `capacity` (capable nodes are full, or the pinned node is briefly offline) and
  `pinned_node_unavailable` (the node holding the task's unpushed work was removed, is offline past 30 min, or
  cannot run the assignee); a QUEUED run nobody can claim shows the node reason instead of `live_run`.

#### Node identity

A node has no token. Its private keys (`state_dir/keys`, `0600`; Ed25519 signs requests, X25519 is for what Core seals
for it) are the only way to act as it towards Core; Core stores the public keys. `factory-node run` refuses to start
without keys and `state_dir/company`, and enrolling needs an `https` `FACTORY_URL` unless Core is on this machine.

```sh
factory nodes enroll --name mini-1                        # owner or admin: a code, valid 15 minutes, single use
FACTORY_URL=... factory-node enroll [-config node.yaml] CODE   # on the node: prints the fingerprint and waits
factory nodes keys                                        # keys and their state (pending, active)
factory nodes approve NODE_ID FINGERPRINT                 # compare first; a wrong fingerprint is refused
factory nodes reject NODE_ID                              # unknown fingerprint: reject and issue a new code
factory nodes revoke NODE_ID                              # below
factory nodes list | factory nodes remove NODE_ID         # remove: an offline, replaced machine (deletes its keys)
```

A node can also connect by signing in (`factory-node login`, `factory nodes connect ID`); installing a node with one
command and the packages Core hands out: [node-install](node-install.md).

Pending keys that are not approved within 24 hours are deleted. A code refused by Core keeps the node's keys, so a
new code does not make a new identity. Enrollment stores the company Core names in `state_dir/company`.

Every node request is signed (RFC 9421 profile in `internal/nodesig`, covering method, path, query and body digest).
Core accepts a signature created within ±120 s of its clock and not before Core started, and each nonce once
(nonces live in Core's memory). Operator and owner tokens and sessions get 401 on node routes.

Revoking deletes the keys: the next request gets 401, the node's runs end as lost so their credentials stop, and
tasks pinned to it are released, and a poll the node has waiting ends with 401.
A machine that lost its keys is revoked and enrolled again.

`FACTORY_URL=... factory-node rotate-keys [-config node.yaml]` writes new keys to `state_dir/keys/next`, proposes them
with a request signed by the current key and waits. Approve with `factory nodes approve NODE_ID NEW_FINGERPRINT`; the
old keys work until then. A running node picks up the new keys without a restart. A rejected or expired proposal
removes `next`, and the current keys stay.

### Restoring Core's database

Core's database identity is its incarnation (`core_meta` row `incarnation`, copy in `core.incarnation` in Core's
data directory, `/data/core.incarnation` in the `core-data` volume). At start Core compares the two; when they
differ (a restored or copied database) it creates a new incarnation and logs `database incarnation differs from
its file: restored backup; new incarnation`. Nodes see it in the next heartbeat answer and drop everything that
belongs to the old one. Runbook (nodes keep running throughout):

1. Stop Core: `docker compose stop core` (Postgres keeps running). Nodes log failed heartbeats and polls and keep
   their runs; their events wait in the nodes' spools.
2. Restore the database from a `pg_dump -Fc` backup into an empty database, keeping Core's current
   `core.incarnation`:

   ```sh
   docker compose exec -T postgres dropdb -U factory --force factory
   docker compose exec -T postgres createdb -U factory factory
   docker compose exec -T postgres pg_restore -U factory -d factory --no-owner <backup/factory.dump
   ```

3. When the backup also brought back its own `core.incarnation` (a copy of the whole data directory), the two
   match and the restore is not detected: write any other value into the file, e.g.
   `docker run --rm -v <project>_core-data:/data busybox sh -c 'echo restored > /data/core.incarnation'`.
4. Start Core: `docker compose start core`; check its log for the `restored backup; new incarnation` line.
   Migrations run as usual when the backup is older than the binary.
5. Nodes: the next heartbeat answer carries the new incarnation (or `404` when the backup predates the node's
   registration or epoch). Each node stops claiming, interrupts and seals all its runs, waits for them to end, then
   registers the next epoch with an empty inventory (`re-registering: stopping all runs` with `Core incarnation
   changed`, then `node registered` with the new incarnation). Spooled events of runs the backup does not know are
   refused and dropped from the spools; only queued runs of the restored database are offered.
6. Check:
   - `curl -s -H "Authorization: Bearer $FACTORY_TOKEN" $FACTORY_URL/nodes`: every node `online`, a higher `epoch`
     than before the restore, its `drivers` `ok`, recent `last_seen` and `health` (disk `ok`).
   - `./bin/factory status` and `./bin/factory runs list`: runs that were active in the backup are now ended by
     Core (the nodes' new epochs lose them): unstarted ones `lost`
     (`lost: not started`, retried), started ones `lost` (`lost: …`, retried). Answer the `inspect` escalations of
     `side_effects` tasks (or `./bin/factory runs resolve`) after inspecting the workspace.
   - Work done after the backup point is not in the database: tasks may run again, and their worktrees keep the
     newer commits (sealed WIP refs and branches stay on the node), so a re-run may start from `workspace_diverged`.
   - `./bin/factory doctor` for Core and its Postgres (`"database": "postgres 17.x"`).

### Task worktrees

With `worktrees.enabled: true`, runs of agents with `workspace.mode: worktree` get one git worktree per task under
`worktrees.root` (default `<state_dir>/worktrees`):

- `<root>/<company>/<project>/repo` is the shared clone of the run's `repo_url`; `tasks/<task>` is the task's worktree
  on branch `af/<task>`; `scratch/<task>` is exported to the runtime as `FACTORY_SCRATCH`.
- Each worktree has its own lock, so a second run of the same task waits; later runs reuse the worktree and its work.
- At run end the runtime process group gets TERM, then KILL after 10 s; the run fails if it survives 30 s. The
  worktree is then sealed: uncommitted work is committed onto `refs/factory/wip/<task>` (the worktree itself is left
  unchanged) and `sealed_head` is written to `.factory/state`. Nothing is pushed. The worker then reports
  `{mode, branch, node_id, exit_sha, sealed_head, sealed_at, unpushed}` to `POST /runs/{id}/workspace` (`node_id` is the node's `name`, in v2 its `node_id`;
  `GET /tasks/{id}/workspace` reads it). Transient failures are retried briefly; a failed report is logged
  and does not fail the run.
- Continuity (task-worktrees D4, 2.2): the seal reads the run's exit file. `exit_sha` is the exit file's
  `exit_sha` when the adapter writes it, else the worktree's HEAD when the worker first saw the exit accepted
  (polled every 250 ms); it is kept in `.factory/state` and reported with every later seal. After an accepted
  `handoff`, `submit` or `done` the worktree is reset to `exit_sha` (`reset --hard` + `clean -fd`; ignored files
  and `.factory/` stay) and `sealed_head = exit_sha`, so the next run starts exactly there. Work after the exit is
  kept on the WIP ref: the snapshot of uncommitted files, or, without one, the commits made after the exit. A reset
  is recorded in `.factory/state` (`reset_from`) before it runs; a crash in between is finished by the next seal
  or start check. After `ask`, `block`, `wait` or no exit the worktree continues as it is. The seal also computes
  whether HEAD has commits on no origin branch (`unpushed`, reported: Core pins the task to this node); `wip_sha`
  is logged.
- `workspace_mode: fresh_from_default` (agent `spec.workspace.mode`): the task worktree has no task branch; every run seals
  what the previous one left, then checks out the freshly fetched `origin/<default>` detached with a clean tree.
  It is reported as mode `worktree` without a branch.
- A turn that ends while the exit file says `pending` (factory-mcp still retrying delivery to an unreachable Core)
  does not kill the runtime's group at once: the worker waits until the exit is `accepted` or `refused`, no
  `factory-mcp` process is left in the group, the run is stopped (revoke, timeout, node stop: killed promptly) or
  60 s (`ExitDeliveryWait`) pass, then stops it as usual; the terminal event's `exit_state` is read after the wait.
  Runs without a pending exit are not delayed.
- A runtime still running 30 s (`ExitGrace`) after its accepted exit is stopped; a run stopped after its accepted
  exit (also by its timeout or a stop) or ending without evidence reports `succeeded`.
- Git lock errors (`index.lock`, `config.lock`, `packed-refs.lock`, ref locks) are retried by the allocator; one
  that persists ends the run `failed` (retryable contention), never `setup_failed`.
- Runs without `task_id` or `repo_url` (for example message runs), or with worktrees disabled, fall back to the shared
  project workspace with a warning. `shared` and `scratch` runs use the project workspace (`workspace.Local`).

- Start check (task-worktrees D4), under the worktree lock before the runtime starts. A lock left by a crashed
  run (pidfile, free lock) is sealed first as that run's work. Then: HEAD equals `sealed_head` (or nothing was
  sealed yet, or the worktree was just recreated) → start, after a fast-forward to `origin/<branch>` when the
  remote is strictly ahead (unpushed local commits or an agent's rebase are kept as they are); `sealed_head`
  follows the fast-forward. HEAD differs from `sealed_head` (changed outside any run), or uncommitted changes
  block the fast-forward → the worktree is left untouched and the run ends `setup_failed` before the runtime starts with
  `workspace_diverged: task … (branch …): <reason>; HEAD …, sealed_head …, origin …`. A missing worktree is
  recreated from the local branch (else the remote branch, else the default branch).

- Reset (task-worktrees 4.2, node side): `./bin/factory-node workspace reset -task ID [-project P] [-config node.yaml]
  [-wait 1m]` takes the worktree lock (waits for a running run up to `-wait`, then fails), stops a leftover run
  group, seals, removes the worktree and scratch directory, points the task branch at `origin/<branch>` (else the
  default branch), recreates the worktree and forgets the task's session records, so the next run starts a fresh
  session. The abandoned local state (the seal's WIP commit, else an unpushed branch head) stays on
  `refs/factory/reset/<task>`; the WIP ref is dropped. It prints the result as JSON and can run next to
  `factory-node run`. The same operation (`janitor.ResetWorkspace`) runs for the task ids in the v2 heartbeat
  answer's `reset` (escalation answers, `pinned_node_unavailable`).

Core opens the `workspace_diverged` escalation and pins tasks with unpushed work to their node at claim
(`pinned_node_unavailable`). Push and PR happen only through explicit
exits (below); merging is the agent's (`task-pull-request` skill). Tasks of one project run in parallel on a node
with `worktrees.enabled` (it registers `worktrees: true`); elsewhere they share the project workspace one at a time.

launchd: `./bin/factory-node launchd [-config node.yaml] [-label com.appfactory.node] > ~/Library/LaunchAgents/com.appfactory.node.plist`
prints a per-user agent for `factory-node run` with `KeepAlive`, `RunAtLoad`, the current directory as
`WorkingDirectory`, logs in `~/Library/Logs/factory-node.log`, a 30 s `ExitTimeOut` for interruption, and the
current `PATH`, `FACTORY_URL`, `CODEX_BIN` and
`CLAUDE_CODE_EXECUTABLE`; no token or key (the node's keys stay in its `state_dir`). Enroll the node
first (`factory-node enroll`, [Node identity](#node-identity)). Load with
`launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.appfactory.node.plist`, stop with
`launchctl bootout gui/$(id -u)/com.appfactory.node`. Run only one node per state directory.

systemd (Linux, node-agent 3b.1): `./bin/factory-node systemd [-config node.yaml] [-unit factory-node] [-system]
[-user USER] [-env-file PATH]` prints a service for `factory-node run` and, on stderr, where to save it. Default is a
user unit (`~/.config/systemd/user/<unit>.service`, `WantedBy=default.target`); `-system` prints a system unit run
as `-user` (default: the current user; `/etc/systemd/system/<unit>.service`, `multi-user.target`, after
`network-online.target`). The unit has `Restart=always`, `RestartSec=10` and `StartLimitIntervalSec=0` (systemd
never gives up; Core counts the epochs), the current directory as `WorkingDirectory`, `KillMode=mixed` with
`TimeoutStopSec=30` (SIGTERM reaches only factory-node, which interrupts its runs and waits; the rest of the
cgroup is killed after 30 s), logs to journald (`SyslogIdentifier=factory-node`), and the current `PATH`,
`FACTORY_URL`, `CODEX_BIN` and `CLAUDE_CODE_EXECUTABLE` as `Environment=`. Secrets are never written into the unit:
`EnvironmentFile=` names an owner-only file (default `~/.config/factory-node/env`, `/etc/factory-node/env` with
`-system`) for any variable that must stay out of the unit (`FACTORY_TOKEN` is refused inline). Enroll the node
before starting the unit:

```sh
install -d -m 700 ~/.config/factory-node
install -m 600 /dev/null ~/.config/factory-node/env   # then add FACTORY_URL=... and other settings
FACTORY_URL=... ./bin/factory-node enroll -config /etc/factory/node.yaml CODE   # and approve it
./bin/factory-node systemd -config /etc/factory/node.yaml > ~/.config/systemd/user/factory-node.service
systemctl --user daemon-reload && systemctl --user enable --now factory-node
loginctl enable-linger "$USER"   # a user unit then runs without a login session
journalctl --user -u factory-node -f
```

Values are escaped for systemd (`%` specifiers, `$` in `ExecStart`, quotes and backslashes); paths must be
absolute, and control characters or a secret in `Environment=` refuse the unit. `systemd-analyze verify` accepts
the output (checked on systemd 252; the unit test runs it when installed).

## OpenSpec in this repository

`openspec/specs/` describes how the system works now; `openspec/changes/` holds only work in progress.
Whoever finishes the last task of a change archives it with `openspec archive NAME` in a separate commit
and fixes any conflict the command reports. A change that modifies a capability is archived after the
change that creates it. Optional leftovers go to `docs/backlog.md`.
