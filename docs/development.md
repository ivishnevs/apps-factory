# Development

Go 1.24.1; nats.go 1.41.2 (compatible with this Go baseline), modernc SQLite 1.36.3,
yaml.v3 3.0.1. SQLite is pure Go; no CGO toolchain is needed in the Core container.
Embedded migrations are internal/storage/migrations/NNNN_name.sql, applied in order, each in its own
transaction, and recorded in schema_migrations; add a migration as the next numbered file. A database
from before schema_migrations is adopted at its PRAGMA user_version, which stays equal to the latest
version so older binaries refuse a newer schema. Core refuses to start on a version it does not know.

Tenancy: every domain table has `company_id` (migration 0008; existing rows belong to `default`), every
`Store` method takes the company right after `ctx`, and transactions carry it in `storage.Tx` so each insert
and each lookup by a caller-supplied ID is scoped. Idempotency keys (`mutation_results`) and agent slots are
per company. Row IDs stay globally unique on SQLite; composite `(company_id, id)` keys come with Postgres.
Core serves one company (`Core.Company`, default `default`); `companyFrom(r)` resolves it per request, and run
credentials bind the company into their HMAC, so a token for another company's run is rejected.
Back up a stopped Core data directory before upgrading; downgrade requires restoring its compatible backup.

`make build`, `make check`, `make up`, `make down`, `make schema`.
`make hooks` (once per clone) enables `.githooks/pre-commit`, which refuses a commit whose staged Go files are not
gofmt-formatted; `make check` also runs `make fmt` (gofmt over `cmd` and `internal`).
FACTORY_ROOT selects config root; FACTORY_DB selects SQLite file; FACTORY_LISTEN selects HTTP bind.
FACTORY_URL is the native CLI/Worker endpoint. NATS_URL is the authenticated broker URL.
CODEX_BIN selects the installed native Codex executable. FACTORY_TOKEN is required.
FACTORY_WORKER_STATE (default data/codex-worker) keeps pidfiles of running runtimes; on start the
Codex Worker kills process groups left there by its previous runs. Do not share it between Workers.
`codex-worker` is `factory-node run --slots 1` with the Codex driver (see Node process).
Core uses signal cancellation, HTTP shutdown deadlines and structured JSON logs.
Worker handles SIGTERM/SIGINT by requesting turn interruption before closing its process group.

Normal tests use fake RPC subprocesses or the internal/driver/fake scripted driver, with no model/network. NATS_TEST_URL enables the integration
test on an isolated broker; the test deletes its streams. scripts/smoke.py is a separately invoked
real ChatGPT smoke test and consumes subscription usage. It verifies start, streaming, completion
and thread resume, and prints only run metadata and requested final text.

Logs: `docker compose logs core`, `docker compose logs nats`; Worker logs to stderr.
Do not print .env or authenticated NATS URLs in reports. Back up both SQLite and the broker volume.
`docker compose down -v` deletes durable state and is not the normal stop command.

Foundation limitations: no automatic worker registration/heartbeat dashboard, no remote machine
path mapping, no event pagination/retention policy, no approval grants yet. Factory MCP enforces run-scoped Core access. Skill operations enforce the shared policy evaluator; shell access is trusted.
Do not proceed to later stages until Foundation checks pass.

Task Engine adds scripts/task-smoke.py (opt-in real model chain; --restart-core tests persistence),
SQLite migration/DAG/version/idempotency/review/retry tests and a JetStream fake-runtime restart test.
See task-lifecycle.md for the supported API/CLI and retry constraints.

Factory MCP builds a native sibling factory-mcp executable. See factory-mcp.md for scoped auth,
stdio tools, audit and the opt-in scripts/mcp-smoke.py planner/executor/reviewer test.

Messaging adds scripts/messaging-smoke.py --restart-core, blocking-answer races, communication scope,
form/duplicate/deadline/escalation/session tests. See messaging.md for HTTP/CLI and MCP contracts.

Action approval verification: `make check` covers exact grants, persistence, denial, expiry, revocation and writer quarantine. `scripts/approvals-smoke.py --restart-core` uses real Codex for approval and rejection with task/thread continuation. See [approvals](approvals.md).

Versioned artifact verification: `scripts/artifact-approvals-smoke.py --restart-core` exercises real Codex registration/submission, independent task review, v1 approval, v2 rejection and a v3 revision unblocking QA. See [artifact approvals](artifact-approvals.md).

Reports and decisions: `scripts/journal-smoke.py --restart-core` verifies three real Codex tasks, pending proposal persistence, independent inbox/task review, explicit replacement and captured run contexts. See [reports and decisions](reports-decisions.md).

OpenSpec uses Go 1.24 os.Root for confined file access, with no new dependency or migration (SQLite remains v7). `scripts/openspec-smoke.py --restart-core` exercises the native official CLI plus the Docker Core gateway and linked tasks. See [OpenSpec](openspec.md).

## Node doctor

`./bin/factory-node doctor` runs the node self-checks locally (node-agent D7); Core and NATS are checked only when
their variables are set. It prints a
STATUS/CHECK/DETAIL table with a remediation hint under every `fail`/`warn` row and exits 1 when any check fails.
Checks: `git` and `git.user`; `forge.auth` (`gh auth status`, warn only) and, with `-repo URL`, `forge.repo`
(`git ls-remote`); `disk` free space at `-disk-path` (default `$HOME`) against `-low-gib 20` (warn) and
`-critical-gib 5` (fail); on macOS `xcode` (`xcode-select -p`, `xcodebuild -version`) and `xcode.simctl` (available
simulators; warn when none); `factory-mcp` and `factory-shim` in `FACTORY_BIN_DIR`, next to the binary, or `./bin`.
`core` (with `FACTORY_URL`): an authenticated `GET /health` with `FACTORY_TOKEN` (fails when the token is unset or
rejected, Core is unreachable, or Core answers 503 because it cannot reach its database or NATS). `nats` (with
`NATS_URL`): one connection without reconnects and a JetStream account info read (fails when unreachable or
JetStream is off). Both are read-only, time out after 5 s, never print the token or URL credentials, and are
`skip` when their variable is unset.
Runtime probes (`node.RuntimeProbe`, binary/version/auth, never a model call; drivers own them and `cmd/factory-node`
injects them as `Doctor.Runtimes`): `codex.*` (`codex.Driver.Probe`: `CODEX_BIN`, exactly
`codex-cli 0.162.1`, `codex login status`) and `claude.*` (Node 22+, built `workers/claude`, `factory-workspace`, and
`claude auth status` of `CLAUDE_CODE_EXECUTABLE` or the `claude` CLI; warn when no CLI is installed, since the
SDK-bundled Claude Code is checked only at run start). By default only installed drivers are probed;
`-drivers codex,claude` requires the listed ones. `drivers` fails when no driver is usable.
Stack hooks from `NodeHooks` arrive with node registration.

## Node process

`./bin/factory-node run [-config node.yaml] [-slots N]` (`make node`) is one process with N slot goroutines
(node-agent 1.1; registration and the inventory heartbeat with `protocol_version: 2`, below; no control subject
yet). Each slot runs the existing executor (`worker.Worker.Process`: claim, ack, execute with per-run heartbeats in
v1); the slots share one pull
subscription per enabled driver on `factory.commands.agent.run.<driver>` with the durable `<driver>-worker-v1`, so they
compete for commands exactly like standalone workers. A free slot polls the drivers in turn, one fetch at a time.
Environment as for the Worker: `NATS_URL`, `FACTORY_URL`, `FACTORY_TOKEN` (required), `CODEX_BIN`. At least one
enabled driver must pass its version check and probe at start (see Driver checks below). SIGINT/SIGTERM interrupt running runs (as the Codex Worker does) and wait
for them. `codex-worker` is the same code with one slot and the Codex driver (`FACTORY_WORKER_STATE` as before).

`node.yaml` comes from `-config`, else `FACTORY_NODE_CONFIG`; without either the defaults apply. Unknown keys are
errors.

```yaml
name: mini-1                 # default: host name; a label only (v2 identifies the node by node_id)
slots: 2                     # 1..64
protocol_version: 1          # 1 (default): per-run claim and heartbeat; 2: registered node (below)
resources: {simulator: 2, build: 2}  # capacity per class, counts >= 0; sent in register, Core enforces at claim
labels: [macos, xcode]       # sent in register
state_dir: data/factory-node # pidfiles, node_id, epoch; never shared by live nodes or workers
bin_dir: /opt/factory/bin    # factory-mcp, factory-shim and ../workers/claude/dist/src/shim.js; default: next to the binary
skills_root: /opt/factory    # configs/skills.yaml + skill dirs materialized for runs; default <bin_dir>/.., else the working directory
drivers:
  codex: {enabled: true, binary: codex}  # binary default: CODEX_BIN, else codex
  claude: {enabled: false}               # Go driver (internal/driver/claude); keys node, shim, executable, allow_api_key
probes: {interval: 10m, version_interval: 1h}  # driver Probe and version check periods (defaults; at least 1m)
disk: {path: data/factory-node, low_watermark_gib: 20, critical_watermark_gib: 5}  # path default: state_dir
worktrees: {enabled: false, root: data/factory-node/worktrees, scan_command: [gitleaks]}  # root default: <state_dir>/worktrees
janitor:                     # trusted node configuration; each command runs with /bin/sh -c
  interval: 10m              # prune period (default 10m)
  hook_timeout: 5m           # per command (default 5m)
  on_run_stop: ['xcrun simctl delete "af-$FACTORY_TASK_ID" || true']    # FACTORY_RUN_ID, FACTORY_TASK_ID, FACTORY_WORKTREE
  on_task_closed: ['xcrun simctl delete "af-$FACTORY_TASK_ID" || true'] # FACTORY_TASK_ID, FACTORY_WORKTREE
  prune:
    - {command: 'find ~/Library/Developer/Xcode/DerivedData -mindepth 1 -maxdepth 1 -mmin +$FACTORY_OLDER_THAN_MINUTES -exec rm -rf {} +', older_than: 72h}
```

Claude stays off by default while `workers/claude` serves `factory.commands.agent.run.claude`; enable it only with
that service stopped (both would share the `claude-worker-v1` consumer).

- Start: runtime process groups recorded in `state_dir` by a previous process are killed (`worker.KillOrphans`).
- Driver checks (runtime-drivers D4): each enabled driver's version check (`CheckVersion`) runs at start and every
  `probes.version_interval`, its `Probe` (binary, version range, authentication; never a model call) at start and
  every `probes.interval`. A failing check disables that driver on this node: free slots stop fetching its
  subject (one error log), the other drivers keep claiming, running runs continue; the next passing check
  re-enables it (one info log). With no usable driver the node claims nothing; the node refuses to start when
  none is usable at start. With `protocol_version: 2` the change is declared to Core by registering the next
  epoch with the driver's `ok` updated, but only at a heartbeat where the node is idle (no busy slot, empty
  inventory), since a new epoch loses every unended run of the older one; a busy node keeps the change local
  until then, and a driver that flipped back meanwhile is not re-registered.
- Disk: before each claim a free slot reads free space on `disk.path`. Below the critical watermark claiming
  pauses (one error log), below the low watermark one warning is logged; both clear with a log line when space
  returns. Running runs continue. `node.low_disk`/`node.disk_critical` events come later.
- Janitor (node-agent D4; hooks live in `node.yaml` until Core serves the catalog's `NodeHooks` to nodes):
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
  the run FAILED before the runtime starts (`skills: … (refused before runtime startup)`). Core does not pin
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

### Runtime protocol v2

With `protocol_version: 2` the node speaks only [runtime protocol v2](runtime-protocol-v2.md) (Core must serve it):

- Identity: `<state_dir>/node_id` is a random id generated on first start and never changed (`name` stays a
  label); a damaged file stops the node instead of creating a new identity. `<state_dir>/epoch` is incremented and
  fsynced on every start and on every re-register. Copying a state dir to a second machine makes both one node:
  Core refuses the older epoch (`409 stale_epoch`) and that process exits.
- Start: orphaned runtime groups are killed, worktrees whose run pidfile outlived the previous process are stopped
  and sealed locally (not reported), then `POST /nodes/register` sends `node_id`, `name`, `epoch`, `slots`,
  `drivers` (`CheckVersion` version, `ok` when no driver probe fails), `resources` and `labels`. Unreachable Core is
  retried (up to 30 s apart); a refusal stops the node.
- Commands are pointers: only the run id is read (`run_id`, or `id` of a full RunSpec during the transition). A
  free slot claims with `{node_id, epoch, claim_id, slot}`; `claim_id` is `<epoch>-<slot>-<n>`, an idempotency key
  reused when a transient failure (network, 5xx) is retried. `409 retry` naks the command with a 5 s delay,
  `409 drop` acks it; on `200` the command is acked and the executor runs the claim's `run_spec`. After the
  workspace is allocated and the runtime launched, `POST /runs/{id}/start {lease}` (retried) moves the run to
  RUNNING; a failed start ends the run UNKNOWN. There are no per-run heartbeats.
- Every `heartbeat_seconds` (register answer, default 10): `POST /nodes/{node_id}/heartbeat` with the inventory
  (`claimed`, `starting`, `running`, `ended_unacked` with `elapsed_ms` since the claim on the monotonic clock)
  and health (`load1`, `cpus`, `free_disk_bytes` and `disk` ok/low/critical on `disk.path` by the node.yaml
  watermarks, `busy_slots`). The answer's `acked` drops ended runs, `stop` cancels runs through the executor
  (which interrupts, seals and reports them; a stopped ended run is dropped), `pause` stops claiming on top of the
  local disk gate, `cleanup` task ids have their worktrees sealed and removed (`git worktree remove`; branch, WIP ref
  and clone stay; a worktree in use is retried on a later listing) or are logged when worktrees are off. A failed
  heartbeat is logged; runs continue.
- A changed `incarnation` (or `404` for the node) stops claiming, stops every run, waits for them to end, then
  registers the next epoch with an empty inventory. `409` on a heartbeat stops the node.

### Task worktrees

With `worktrees.enabled: true`, runs of agents with `workspace.mode: worktree` get one git worktree per task under
`worktrees.root` (default `<state_dir>/worktrees`):

- `<root>/default/<project>/repo` is the shared clone of the run's `repo_url`; `tasks/<task>` is the task's worktree
  on branch `af/<task>`; `scratch/<task>` is exported to the runtime as `FACTORY_SCRATCH`.
- Each worktree has its own lock, so a second run of the same task waits; later runs reuse the worktree and its work.
- At run end the runtime process group gets TERM, then KILL after 10 s; the run fails if it survives 30 s. The
  worktree is then sealed: uncommitted work is committed onto `refs/factory/wip/<task>` (the worktree itself is left
  unchanged) and `sealed_head` is written to `.factory/state`. Nothing is pushed. The worker then reports
  `{mode, branch, node_id, sealed_head, sealed_at}` to `POST /runs/{id}/workspace` (`node_id` is the node's `name`, in v2 its `node_id`;
  `GET /tasks/{id}/workspace` reads it). Transient failures are retried briefly; a failed report is logged
  and does not fail the run.
- Runs without `task_id` or `repo_url` (for example message runs), or with worktrees disabled, fall back to the shared
  project workspace with a warning. `shared` and `scratch` runs use the project workspace (`workspace.Local`).

- Start check (task-worktrees D4), under the worktree lock before the runtime starts. A lock left by a crashed
  run (pidfile, free lock) is sealed first as that run's work. Then: HEAD equals `sealed_head` (or nothing was
  sealed yet, or the worktree was just recreated) → start, after a fast-forward to `origin/<branch>` when the
  remote is strictly ahead (unpushed local commits or an agent's rebase are kept as they are); `sealed_head`
  follows the fast-forward. HEAD differs from `sealed_head` (changed outside any run), or uncommitted changes
  block the fast-forward → the worktree is left untouched and the run ends FAILED before the runtime starts with
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

Not yet: push, PR, reset to `exit_sha` (`exit_sha` is not reported yet), the `workspace_diverged` escalation. The SQLite writer indexes still serialize runs of one project.

launchd: `./bin/factory-node launchd [-config node.yaml] [-label com.appfactory.node] > ~/Library/LaunchAgents/com.appfactory.node.plist`
prints a per-user agent for `factory-node run` with `KeepAlive`, `RunAtLoad`, the current directory as
`WorkingDirectory`, logs in `~/Library/Logs/factory-node.log`, a 30 s `ExitTimeOut` for interruption, and the
current `PATH`, `NATS_URL`, `FACTORY_URL`, `FACTORY_TOKEN`, `CODEX_BIN` and `CLAUDE_CODE_EXECUTABLE` (the token is a
placeholder when unset). The plist then holds the token: `chmod 600` it. Load with
`launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.appfactory.node.plist`, stop with
`launchctl bootout gui/$(id -u)/com.appfactory.node`. Run only one of the node or a standalone `codex-worker` per
state directory.
