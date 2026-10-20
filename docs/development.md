# Development

Go 1.24.1; nats.go 1.41.2 (compatible with this Go baseline), pgx 5.7.5 (`pgx/v5/stdlib` behind
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

Tenancy: accounts own companies and users; `memberships` give a user a role (`owner`, `member`) in companies of
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
line of defence. The outbox publisher (`Store.PublishOutbox`) takes up to 100 due rows `FOR UPDATE SKIP LOCKED`
in a short transaction, publishes each with its outbox ID as the JetStream message ID and marks it sent in the
same transaction: several Core processes publish in parallel, each row once; a crash before the commit republishes
the same IDs (deduplicated by JetStream, idempotent at consumers).

Catalog per company: `configs/` is the `default` company's catalog; another company's lives in
`configs/<company>/` with the same layout (`agents/`, `profiles/`, `policies/`, `node-hooks/`, `quota_keys.yaml`,
`company.yaml`). `configs/skills.yaml` and `configs/prices.yaml` are shared, prompt files are paths relative to
the root. `factory config validate --company X` (or `FACTORY_COMPANY=X`) checks one company's catalog; agents of
the same name in two companies are independent.

NATS subjects carry the company: `factory.<company>.commands.agent.run.<driver>` (stream `FACTORY_COMMANDS`,
`factory.*.commands.>`), `factory.<company>.events.<kind>` and `factory.<company>.feed.<project>.<kind>` (stream
`FACTORY_EVENTS`, `factory.*.events.>` and `factory.*.feed.>`), and the plain control subject
`factory.<company>.nodes.<node_id>.control`. Core consumes its company's events with the durable
`core-events-<company>`; executors share `<driver>-worker-<company>` per driver. Core updates the streams' subjects
at start; consumers of the old unprefixed subjects are not reused (upgrade workers, nodes and Core together).

Backups: back up the Postgres database (`pg_dump`, below), the blob store (`FACTORY_BLOB_DIR`, default
`<data dir>/blobs`, the `core-blobs` volume: OpenSpec documents, artifact contents, attachments and run logs by
digest; back it up after the database, a blob newer than the dump is harmless) and the NATS volume; Core's data
directory (`FACTORY_DATA_DIR`, default `data`, the `core-data` volume) holds only `core.incarnation`. Core mounts
no project workspace. Back up before upgrading; a downgrade requires restoring a compatible backup (an older Core refuses a newer schema).

```sh
docker compose exec -T postgres pg_dump -U factory -Fc factory >backup/factory.dump   # online, consistent
```

`make build`, `make check`, `make up`, `make down`, `make schema`, `make test-services`, `make test-services-down`.
`make hooks` (once per clone) enables `.githooks/pre-commit`, which refuses a commit whose staged Go files are not
gofmt-formatted; `make check` also runs `make fmt` (gofmt over `cmd` and `internal`).
FACTORY_ROOT selects config root; FACTORY_DATABASE_URL the database; FACTORY_COMPANY the company Core serves;
FACTORY_DATA_DIR Core's data directory; FACTORY_BLOB_DIR Core's blob store; FACTORY_LISTEN selects HTTP bind.
FACTORY_URL is the native CLI/Worker endpoint. NATS_URL is the authenticated broker URL.
CODEX_BIN selects the installed native Codex executable. FACTORY_TOKEN is required.
FACTORY_WORKER_STATE (default data/codex-worker) keeps pidfiles of running runtimes; on start the
Codex Worker kills process groups left there by its previous runs. Do not share it between Workers.
`codex-worker` is `factory-node run --slots 1` with the Codex driver (see Node process).
Core uses signal cancellation, HTTP shutdown deadlines and structured JSON logs.
Worker handles SIGTERM/SIGINT by requesting turn interruption before closing its process group.

Tests: every test that touches storage needs Postgres, like the JetStream tests need NATS. `make test-services`
starts both in tmpfs containers (Postgres on 127.0.0.1:15432, NATS on 127.0.0.1:14231); the Makefile exports
`FACTORY_TEST_DATABASE_URL=postgres://postgres:factory@127.0.0.1:15432/postgres?sslmode=disable` (export it yourself
for a plain `go test`); set `NATS_TEST_URL=nats://127.0.0.1:14231` for the JetStream tests and run them with
`-p 1` (packages share the streams). Without these variables those tests are
skipped with a message. Each test gets its own database cloned from a template database built once per schema
(`ft_<schema hash>`, kept on the server; `internal/storage/pgtest`, `storagetest.Open(t)`), and dropped at the
end. Use a server you can throw away: tests create and drop databases on it. Full suite:
`go test -p 1 -count=1 ./...`.

Normal tests use fake RPC subprocesses or the internal/driver/fake scripted driver, with no model/network. NATS_TEST_URL enables the integration
test on an isolated broker; the test deletes its streams. scripts/smoke.py is a separately invoked
real ChatGPT smoke test and consumes subscription usage. It verifies start, streaming, completion
and thread resume, and prints only run metadata and requested final text.

Logs: `docker compose logs core`, `docker compose logs postgres`, `docker compose logs nats`; Worker logs to stderr.
Do not print .env, database URLs with passwords or authenticated NATS URLs in reports. Back up both Postgres and
the broker volume.
`docker compose down -v` deletes durable state and is not the normal stop command.

Foundation limitations: no automatic worker registration/heartbeat dashboard, no remote machine
path mapping, no event pagination/retention policy, no approval grants yet. Factory MCP enforces run-scoped Core access. Skill operations enforce the shared policy evaluator; shell access is trusted.
Do not proceed to later stages until Foundation checks pass.

Task Engine adds scripts/task-smoke.py (opt-in real model chain; --restart-core tests persistence),
migration/DAG/version/idempotency/review/retry tests and a JetStream fake-runtime restart test.
See task-lifecycle.md for the supported API/CLI and retry constraints.

Factory MCP builds a native sibling factory-mcp executable. See factory-mcp.md for scoped auth,
stdio tools, audit and the opt-in scripts/mcp-smoke.py planner/executor/reviewer test.

Messaging adds scripts/messaging-smoke.py --restart-core, blocking-answer races, communication scope,
form/duplicate/deadline/escalation/session tests. See messaging.md for HTTP/CLI and MCP contracts.

Action approval verification: `make check` covers exact grants, persistence, denial, expiry, revocation and writer quarantine. `scripts/approvals-smoke.py --restart-core` uses real Codex for approval and rejection with task/thread continuation. See [approvals](approvals.md).

Versioned artifact verification: `scripts/artifact-approvals-smoke.py --restart-core` exercises real Codex registration/submission, independent task review, v1 approval, v2 rejection and a v3 revision unblocking QA. See [artifact approvals](artifact-approvals.md).

Reports and decisions: `scripts/journal-smoke.py --restart-core` verifies three real Codex tasks, pending proposal persistence, independent inbox/task review, explicit replacement and captured run contexts. See [reports and decisions](reports-decisions.md).

OpenSpec documents live in Core (migration `0002_project_data`: `spec_documents`, blobs); nodes materialize them and the MCP adapter syncs edits back at exits. `factory openspec import PROJECT DIR` uploads an existing tree. `scripts/openspec-smoke.py --restart-core` exercises the native official CLI on a downloaded copy, owner compare-and-set writes and linked tasks. See [OpenSpec](openspec.md).

## Node doctor

`./bin/factory-node doctor` runs the node self-checks locally (node-agent D7); Core and NATS are checked only when
their variables are set. It prints a
STATUS/CHECK/DETAIL table with a remediation hint under every `fail`/`warn` row and exits 1 when any check fails.
Checks: `git` and `git.user`; `forge.auth` (`gh auth status`, warn only) and, with `-repo URL`, `forge.repo`
(`git ls-remote`); `disk` free space at `-disk-path` (default `$HOME`) against `-low-gib 20` (warn) and
`-critical-gib 5` (fail); on macOS `xcode` (`xcode-select -p`, `xcodebuild -version`) and `xcode.simctl` (available
simulators; warn when none); `factory-mcp` and `factory-shim` in `FACTORY_BIN_DIR`, next to the binary, or `./bin`;
`ps` (`ps -o lstart= -p <pid>` must print a start time: runtime process groups are recorded by their leader's start
time so a stop never signals a reused PID; without it a stop kills only the runtime's leader and can orphan its
children, and the node logs that once; hint: install procps).
`core` (with `FACTORY_URL`): an authenticated `GET /health` with the node token as `run` resolves it
(`FACTORY_NODE_TOKEN`, node.yaml `token_file` of `-config`, else `FACTORY_TOKEN`; fails when no token is set or it is
rejected, Core is unreachable, or Core answers 503 because it cannot reach its database or NATS). `nats` (with
`nats.url` of `-config node.yaml` / `FACTORY_NODE_CONFIG`, else `NATS_URL`): one connection without reconnects
with the node's own NATS settings (credentials, nkey, TLS; see [Outbound NATS](#outbound-nats-tls-websocket-per-node-user))
and a JetStream account info read (fails when unreachable, the user or certificate is rejected, or JetStream is
off); `nats_credentials` warns when the creds, nkey seed or TLS key file is readable by group or others. Both are
read-only, time out after 5 s, never print the token, URL credentials or file contents, and are `skip` when their
variable is unset.
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
(node-agent 1.1; registration and the inventory heartbeat with `protocol_version: 2`, below; v2 nodes also
subscribe to their control subject `factory.<company>.nodes.<node_id>.control`). Each slot runs the existing executor (`worker.Worker.Process`: claim, ack, execute with per-run heartbeats in
v1); the slots share one pull
subscription per enabled driver on `factory.<company>.commands.agent.run.<driver>` with the durable
`<driver>-worker-<company>` (node.yaml `company`, default `default`; a v2 node refuses to run when the register answer names another
company for its node token), so they
compete for commands exactly like standalone workers. A free slot polls the drivers in turn, one fetch at a time.
Environment as for the Worker: `NATS_URL` (when `nats.url` is unset), `FACTORY_URL`, `CODEX_BIN`, and the node's
credential (required): `FACTORY_NODE_TOKEN`, else node.yaml `token_file`, else the shared `FACTORY_TOKEN` (see
Node tokens below). At least one
enabled driver must pass its version check and probe at start (see Driver checks below). SIGINT/SIGTERM interrupt running runs (as the Codex Worker does) and wait
for them. `codex-worker` is the same code with one slot and the Codex driver (`FACTORY_WORKER_STATE` as before).

`node.yaml` comes from `-config`, else `FACTORY_NODE_CONFIG`; without either the defaults apply. Unknown keys are
errors.

```yaml
name: mini-1                 # default: host name; a label only (v2 identifies the node by node_id)
slots: 2                     # 1..64
protocol_version: 1          # 1 (default): per-run claim and heartbeat; 2: registered node (below)
resources: {simulator: 2, build: 2}  # capacity per class, counts >= 0; sent in register, Core enforces at claim
labels: [macos, xcode]       # sent in register; Core claims an agent's run only where all its spec.labels are
token_file: /etc/factory/node.token  # node token (factory nodes token create); FACTORY_NODE_TOKEN overrides
runtime_env: [XCODE_*, MY_TOOL_HOME]  # extra variables agent runtimes inherit (allowlist; credentials never pass)
state_dir: data/factory-node # pidfiles, node_id, epoch; never shared by live nodes or workers
bin_dir: /opt/factory/bin    # factory-mcp, factory-shim and ../workers/claude/dist/src/shim.js; default: next to the binary
skills_root: /opt/factory    # configs/skills.yaml + skill dirs materialized for runs; default <bin_dir>/.., else the working directory
drivers:
  codex: {enabled: true, binary: codex}  # binary default: CODEX_BIN, else codex
  claude: {enabled: false}               # Go driver (internal/driver/claude); keys node, shim, executable, allow_api_key
probes: {interval: 10m, version_interval: 1h}  # driver Probe and version check periods (defaults; at least 1m)
disk: {path: data/factory-node, low_watermark_gib: 20, critical_watermark_gib: 5}  # path default: state_dir
worktrees: {enabled: false, root: data/factory-node/worktrees, scan_command: [gitleaks]}  # root default: <state_dir>/worktrees
nats:                        # outbound connection (below); without url: NATS_URL, else nats://127.0.0.1:4222
  url: wss://nats.example.com:443  # nats://, tls://, ws:// or wss://, comma list allowed; no user info here
  creds_file: /etc/factory/node.creds    # or nkey_seed_file: /etc/factory/node.nk (exclusive)
  tls: {ca_file: /etc/factory/nats-ca.pem, server_name: nats.example.com}  # cert_file + key_file for mutual TLS
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
  every `probes.interval`. A failing check disables that driver on this node: free slots stop fetching its
  subject (one error log), the other drivers keep claiming, running runs continue; the next passing check
  re-enables it (one info log). A run that ends on the runtime's authentication failure (`driver.AuthFailure`:
  Claude `auth_error`, Codex without a ChatGPT login; such runs end `paused_out`) disables its driver the same
  way at once, without waiting for the next probe, for a hold-off of 10 min, then 30 min, then 2 h (cap) for each
  further auth failure; a passing probe re-enables it only after the hold-off (one info log when a probe passes
  during it), so a probe that passes while runs keep failing auth does not flap the driver. Failures of runs that
  were in flight while it is disabled do not escalate; the escalation starts over after a run on that driver
  completes, or after it ran 2 h past its last hold-off without an auth failure (runtime-drivers 3.2).
  With no usable driver the node claims nothing; the node refuses to start when
  none is usable at start. With `protocol_version: 2` every inventory heartbeat carries the drivers (`ok`, and for
  a disabled one the failing `check` — `version`, `probe` or `auth` — with its `detail`), and a flip sends a
  heartbeat at once: Core updates the node's drivers without a new epoch, claims follow them, and Core records one
  `driver_unsupported` attention event per driver that becomes unusable (`driver_restored` when it is usable again).
- Disk: before each claim a free slot reads free space on `disk.path`. Below the critical watermark claiming
  pauses (one error log), below the low watermark one warning is logged; both clear with a log line when space
  returns. Running runs continue. With protocol v2 the heartbeat's `health.disk` makes Core raise one
  `node.low_disk` per low episode and one `node.disk_critical` (and answer `pause`) per critical one.
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

- Skills (runtime-drivers 2.3): a run's offered skills (`RunSpec.skills`) are copied from the node's registry
  (`skills_root`/`configs/skills.yaml`; without it runs get no skills, logged at start) into a temporary directory
  outside the workspace, and each copy's content hash (`sha256:` over the skill directory's regular files and
  names, `skills.Hash`) is checked against the hash pinned on the run. A mismatch, or a skill this node lacks, ends
  the run `setup_failed` before the runtime starts (`skills: … (refused before runtime startup)`). Core does not pin
  hashes yet: the executor reads `skill_hashes` from the agent snapshot when present and otherwise logs the
  computed hash and materializes it. Claude: the copies go to `<cwd>/.claude/skills/<name>` (read with the
  `project` setting source), marked with `.factory-skill`, added to the repository's `info/exclude` and removed when
  the session closes; a project's own skill with the same name refuses the launch. Codex: the app-server gets the
  copy directory through `skills/extraRoots/set` before the thread starts (verified with codex-cli 0.162.1;
  skills of `CODEX_HOME` stay visible as before).
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

### Outbound NATS (TLS, WebSocket, per-node user)

node-agent 1.5, node side: the node only dials out, so a machine needs no inbound port. `nats.url` takes
`nats://`, `tls://` (TCP with TLS), `ws://` or `wss://` (WebSocket, e.g. through an HTTPS reverse proxy). The user is
a per-node NATS user from `creds_file` (a `.creds` file with user JWT and seed, re-read on every reconnect, for an
operator/JWT setup) or `nkey_seed_file` (an nkey user seed; the server lists the public key). Any `tls` key makes TLS
required: `ca_file` replaces the system roots (private CA), `server_name` is the name expected in the server
certificate (when the URL is an IP or a proxy address), `cert_file`/`key_file` present a client certificate. Secrets
come only from files: `nats.url` in `node.yaml` must not carry user info (the `NATS_URL` fallback still may, as for
the local Worker), errors and logs name files, never their contents, and the start log shows the redacted URL, the
auth kind (`creds`, `nkey`, `url`, `none`) and whether TLS is on. Keep the files `0600` (doctor warns otherwise).
`tls` with `ws://` is refused (use `wss://`). Core keeps `NATS_URL` (`bus.Connect`); nodes and `codex-worker` use
`bus.ConnectWith(url, bus.Options)`.

The node's user needs (runtime protocol v1/v2, company `<company>`): publish `$JS.API.INFO`,
`$JS.API.STREAM.NAMES`, `$JS.API.STREAM.INFO.FACTORY_COMMANDS`, `$JS.API.STREAM.INFO.FACTORY_EVENTS`,
`$JS.API.CONSUMER.CREATE.FACTORY_COMMANDS.>`, `$JS.API.CONSUMER.DURABLE.CREATE.FACTORY_COMMANDS.>`,
`$JS.API.CONSUMER.INFO.FACTORY_COMMANDS.>`, `$JS.API.CONSUMER.MSG.NEXT.FACTORY_COMMANDS.>`,
`$JS.ACK.FACTORY_COMMANDS.>`, `factory.<company>.events.agent.>`; subscribe `_INBOX.>` and its own control subject
`factory.<company>.nodes.<node_id>.control` only. The streams must exist (Core creates them); a node user cannot
create streams, publish commands or subscribe to another node's control subject (`scripts/nats-secure-test.sh`
issues two such node users; `TestNodeControlSubjectIsPerNode`). Narrowing the consumer API to one company's
durables and a per-node inbox prefix are infra work. Example server configuration for a TLS client port and a TLS WebSocket listener with nkey users
(the local `compose.yaml` is unchanged):

```
listen: 0.0.0.0:4222
jetstream { store_dir: /data }
tls { cert_file: /certs/server.pem, key_file: /certs/server-key.pem }
websocket { port: 8443, tls { cert_file: /certs/server.pem, key_file: /certs/server-key.pem } }
authorization {
  users = [
    { nkey: UCORE... }                      # Core: all subjects
    { nkey: UNODE..., permissions: { publish: { allow: [ "$JS.API.INFO", ... the list above ... ] }
                                    subscribe: { allow: [ "_INBOX.>", "factory.default.nodes.<node_id>.control" ] } } }
  ]
}
```

`scripts/nats-secure-test.sh up` starts two throwaway servers (JetStream in tmpfs; generated CA, server certificate
for `nats.test`, nkey users `admin` and `node` with exactly that permission set; and an operator-mode server with a
`.creds` user) in `.nats-secure-test/` and prints the `NATS_TEST_SECURE_*`, `NATS_TEST_ADMIN_SEED`,
`NATS_TEST_NODE_SEED` and `NATS_TEST_CREDS_*` exports for `TestSecureConnect`, `TestCredsConnect`
(`internal/bus`) and `TestNATSCheckSecure` (`internal/node`); `scripts/nats-secure-test.sh down` removes them.
The directory must be shared with the Docker VM (Colima/Docker Desktop share `$HOME`).

### Explicit exits on the node

With Core's `FACTORY_EXPLICIT_EXITS=1` runs end with `factory_exit_*` ([contract](explicit-exits-contract.md)).
`factory-mcp` then needs `git` (and `gh` for pull requests) on the node; it collects the exit facts in the run's
working directory, refuses a dirty tree or a scan finding locally, pushes the task branch and opens its pull request
before `submit`/`handoff`, and writes the exit state to `FACTORY_EXIT_FILE`. The executor reports it on the terminal
event, stops a runtime that keeps running 30 s after its exit, and turns on the exit guard (Codex follow-up turn,
Claude Stop hook, 2 attempts each).

### Runtime protocol v2

With `protocol_version: 2` the node speaks only [runtime protocol v2](runtime-protocol-v2.md) (Core must serve it):

- Identity: `<state_dir>/node_id` is a random id generated on first start and never changed (`name` stays a
  label); a damaged file stops the node instead of creating a new identity. `<state_dir>/epoch` is incremented and
  fsynced on every start and on every re-register. Copying a state dir to a second machine makes both one node:
  Core refuses the older epoch (`409 stale_epoch`) and that process exits.
- Start: orphaned runtime groups are killed, worktrees whose run pidfile outlived the previous process are stopped
  and sealed locally (not reported), then `POST /nodes/register` sends `node_id`, `name`, `epoch`, `slots`,
  `drivers` (`CheckVersion` version, `ok` when no driver check fails, else `check` and `detail`), `resources`
  and `labels`. Unreachable Core is retried (up to 30 s apart); a refusal stops the node.
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
  and clone stay; a worktree in use is retried on a later listing) or are logged when worktrees are off. A failed
  heartbeat is logged; runs continue.
- A changed `incarnation` (or `404` for the node) stops claiming, stops every run, waits for them to end, then
  registers the next epoch with an empty inventory. `409` on a heartbeat stops the node.
- Capacity (Core): a run holds its slot and resource classes from the claim until it has ended in Core **and** the
  node no longer lists it unended; the node lists a run `ended_unacked` only after its seal and run-stop hooks, so
  an ended but unsealed run still holds its class. A new epoch releases what the old process held. Agents'
  `spec.labels` must all be node labels; a shortfall answers `409 {"retry":true,"reason":"capacity","detail":…}`.
  `factory tasks why` shows `no_capable_node` (no online, unpaused node has the assignee's driver, labels and
  resources), `capacity` (capable nodes are full, or the pinned node is briefly offline) and
  `pinned_node_unavailable` (the node holding the task's unpushed work was removed, is offline past 30 min, or
  cannot run the assignee); a QUEUED run nobody can claim shows the node reason instead of `live_run`.

#### Node tokens

Each machine gets its own token (node-agent 1.1), issued by the owner and shown once:

```sh
factory nodes token create mini-1 [--node-id NODE_ID]   # {id, token: fnt_…}; Core keeps only its SHA-256
factory nodes token list                                # ids, names, bound node_id, last use (no secrets)
factory nodes token revoke TOKEN_ID                     # the node gets 401 from the next request
factory nodes list | factory nodes remove NODE_ID       # remove: an offline, replaced machine (revokes its tokens)
```

Put it in a 0600 file named by node.yaml `token_file` (or `FACTORY_NODE_TOKEN` in the systemd `EnvironmentFile` /
launchd plist). A token without `--node-id` is bound to the `node_id` of its first register; afterwards it
authenticates only that node's routes: register, its own heartbeat, v2 claims naming it, and `start`,
`workspace`, `credential` of runs it claimed (`GET /health` too); every other route answers 403. The shared
`FACTORY_TOKEN` keeps working for nodes during the transition.

### Restoring Core's database

Core's database identity is its incarnation (`core_meta` row `incarnation`, copy in `core.incarnation` in Core's
data directory, `/data/core.incarnation` in the `core-data` volume). At start Core compares the two; when they
differ (a restored or copied database) it creates a new incarnation and logs `database incarnation differs from
its file: restored backup; new incarnation`. Nodes see it in the next heartbeat answer and drop everything that
belongs to the old one. Runbook (protocol v2 nodes keep running throughout; v1 workers such as `codex-worker` have
no incarnation: stop them first and start them after step 5):

1. Stop Core: `docker compose stop core` (Postgres and NATS keep running). Nodes log failed heartbeats and keep
   their runs.
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
   changed`, then `node registered` with the new incarnation). Commands still queued in NATS for runs the backup
   does not know start nothing: their claim is refused (`404`; the node logs `claim failed` and redelivers them).
6. Check:
   - `curl -s -H "Authorization: Bearer $FACTORY_TOKEN" $FACTORY_URL/nodes`: every node `online`, a higher `epoch`
     than before the restore, its `drivers` `ok`, recent `last_seen` and `health` (disk `ok`).
   - `./bin/factory status` and `./bin/factory runs list`: runs that were active in the backup are now ended by
     Core (the nodes' new epochs lose them): unstarted ones `lost`
     (`lost: not started`, retried), started ones `lost` (`lost: …`, retried). Answer the `inspect` escalations of
     `side_effects` tasks (or `./bin/factory runs resolve`) after inspecting the workspace.
   - Work done after the backup point is not in the database: tasks may run again, and their worktrees keep the
     newer commits (sealed WIP refs and branches stay on the node), so a re-run may start from `workspace_diverged`.
   - `./bin/factory doctor` for Core, its Postgres (`"database": "postgres 17.x"`) and NATS.

### Task worktrees

With `worktrees.enabled: true`, runs of agents with `workspace.mode: worktree` get one git worktree per task under
`worktrees.root` (default `<state_dir>/worktrees`):

- `<root>/default/<project>/repo` is the shared clone of the run's `repo_url`; `tasks/<task>` is the task's worktree
  on branch `af/<task>`; `scratch/<task>` is exported to the runtime as `FACTORY_SCRATCH`.
- Each worktree has its own lock, so a second run of the same task waits; later runs reuse the worktree and its work.
- At run end the runtime process group gets TERM, then KILL after 10 s; the run fails if it survives 30 s. The
  worktree is then sealed: uncommitted work is committed onto `refs/factory/wip/<task>` (the worktree itself is left
  unchanged) and `sealed_head` is written to `.factory/state`. Nothing is pushed. The worker then reports
  `{mode, branch, node_id, exit_sha, sealed_head, sealed_at}` to `POST /runs/{id}/workspace` (`node_id` is the node's `name`, in v2 its `node_id`;
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
  whether HEAD has commits on no origin branch (`unpushed`); with `wip_sha` it is logged for node pinning (no
  report field yet).
- `workspace_mode: fresh_from_default` (no Core config yet): the task worktree has no task branch; every run seals
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
  `factory-node run`. The same operation is `janitor.ResetWorkspace` for when Core sends reset-workspace to
  nodes (no protocol field yet).

Core opens the `workspace_diverged` escalation and pins tasks with unpushed work to their node at claim
(`pinned_node_unavailable`). Not yet: `fresh_from_default` in agent config. Push and PR happen only through explicit
exits (below); merging is the agent's (`task-pull-request` skill). Tasks of one project run in parallel on a node
with `worktrees.enabled` (it registers `worktrees: true`); elsewhere they share the project workspace one at a time.

launchd: `./bin/factory-node launchd [-config node.yaml] [-label com.appfactory.node] > ~/Library/LaunchAgents/com.appfactory.node.plist`
prints a per-user agent for `factory-node run` with `KeepAlive`, `RunAtLoad`, the current directory as
`WorkingDirectory`, logs in `~/Library/Logs/factory-node.log`, a 30 s `ExitTimeOut` for interruption, and the
current `PATH`, `NATS_URL`, `FACTORY_URL`, `FACTORY_TOKEN`, `FACTORY_NODE_TOKEN`, `CODEX_BIN` and
`CLAUDE_CODE_EXECUTABLE` (`FACTORY_TOKEN` is a placeholder when neither token is set). The plist then holds the token: `chmod 600` it. Load with
`launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.appfactory.node.plist`, stop with
`launchctl bootout gui/$(id -u)/com.appfactory.node`. Run only one of the node or a standalone `codex-worker` per
state directory.

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
`-system`) holding `FACTORY_NODE_TOKEN=…` (or the shared `FACTORY_TOKEN=…`) and `NATS_URL=…` (it may carry the
NATS password):

```sh
install -d -m 700 ~/.config/factory-node
install -m 600 /dev/null ~/.config/factory-node/env   # then add FACTORY_NODE_TOKEN=... and NATS_URL=...
./bin/factory-node systemd -config /etc/factory/node.yaml > ~/.config/systemd/user/factory-node.service
systemctl --user daemon-reload && systemctl --user enable --now factory-node
loginctl enable-linger "$USER"   # a user unit then runs without a login session
journalctl --user -u factory-node -f
```

Values are escaped for systemd (`%` specifiers, `$` in `ExecStart`, quotes and backslashes); paths must be
absolute, and control characters or a secret in `Environment=` refuse the unit. `systemd-analyze verify` accepts
the output (checked on systemd 252; the unit test runs it when installed).
