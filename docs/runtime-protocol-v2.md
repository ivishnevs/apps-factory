# Runtime protocol v2

Status: agreed (2026-10-14) after stream B's review; implemented on both sides (Core: §11) and run together in
`TestJetStreamNodeV2RealCore`. Consolidates `node-agent` D2–D3, `reliable-recovery` D6–D7 and
`trigger-log-scheduling` D4/D6 into one Core ↔ node contract so both sides can build against it. Nothing here
changes the current (v1) behaviour until a node registers with `protocol_version: 2`.

## 1. Versions and transition

- v1 (today): per-run `POST /runs/{id}/claim` (its answer carries the RunSpec with the brief, as in v2; a worker
  facing an older Core falls back to `GET /runs/{id}`) → `POST /runs/{id}/heartbeat`; the command payload is
  still the full RunSpec, but the worker takes only its id.
- v2: a node registers, claims in two phases, renews all its leases with one inventory heartbeat, and treats the
  command as a pointer.
- Core serves v1 and v2 side by side. A registered node speaks only v2 (no mixing on one node); unregistered workers
  keep v1. A run claimed through v2 is renewed only by its node's heartbeat; a v1 run only by its per-run heartbeat.
  v1 is removed one release after every node runs v2.

## 2. Identities

| Name | Meaning | Source |
|---|---|---|
| `node_id` | one per machine and company, stable across renames | generated once, stored in `<state_dir>/node_id` |
| `name` | human label | `node.yaml` `name` (default host name); Core warns when two online nodes share it |
| `epoch` | increments on every node process start, never reused | integer in `<state_dir>/epoch`, written before register |
| `slot` | slot index 0..slots-1 | node |
| `claim_id` | idempotency key of one claim attempt; no ordering (slots claim concurrently) | node, unique within the epoch |
| `lease` | per-run secret returned by claim | Core |
| `incarnation` | Core database identity; changes when a backup is restored | Core |

Nodes authenticate with a per-machine **node token** (`Authorization: Bearer fnt_…`, issued by the owner,
company-scoped, bound to one `node_id`; §11); the shared worker token keeps working during the transition. Every
request carries `node_id`.

## 3. Register

`POST /nodes/register`

```json
{"node_id": "6f0c…", "name": "mini-1", "epoch": 7, "protocol_version": 2, "slots": 3,
 "drivers": [{"name": "codex", "version": "codex-cli 0.162.1", "ok": true},
             {"name": "claude", "version": "2.1.284", "ok": false, "check": "auth", "detail": "not logged in"}],
 "resources": {"simulator": 2, "build": 2}, "labels": ["macos", "xcode"],
 "hooks": "ios", "reason": "start|reregister"}
```

Answer `200 {"incarnation": "…", "heartbeat_seconds": 10, "start_window_seconds": 60, "hooks": {"name": "ios", "hash": "…", "missing": false, "spec": {…}}}`.

`drivers` come from each driver's `Probe`; `name` is exactly the provider (`codex`, `claude`); a driver that is not
`ok` names the failing `check` (`version`, `probe`, `auth`) and its `detail`; `until` (Unix seconds) is the end
of a run's auth failure hold-off, before which a passing probe does not re-enable it, and `transient_failures`
counts the checks in a row that failed only transiently (the driver stays as it was until
`probes.transient_failures` of them). `resources` is the capacity from
`node.yaml`. `hooks` (optional, node.yaml `hooks`, default `default`) names the company catalog's `NodeHooks`
document; the answer's `hooks` carries it (`spec`: `on_run_stop`, `on_task_closed`, `prune`
`[{command, older_than}]`, `checks` `[{name, command, hint, labels}]`; `missing: true` with an empty spec when the
catalog has no such document). `reason` (optional, default `start`) says why the node registers: `start` (a process
start) or `reregister` (Core lost the node or changed incarnation); only `start` counts towards
`node.restart_loop`. A driver change never re-registers: the heartbeat's `drivers` declare it (§3).

Effects: the node is online; every unended run of an older epoch of this node is lost (§10 "Lost runs"). A lower or equal epoch than
the last registered one is refused with `409 {"reason": "stale_epoch"}`. Before registering, the node kills and
seals what its previous epoch left (it already kills orphaned groups at start).

## 4. Command

Subject `factory.<company>.commands.agent.run.<provider>` (node.yaml `company`; the register answer's `company`, the
node token's, must match it). Payload becomes a pointer:

```json
{"run_id": "…", "company": "default", "agent": "calorie-ios", "provider": "codex"}
```

v2 nodes take only `run_id` from the command; everything else comes from claim. Core keeps sending the full RunSpec
in the payload during the transition so v1 workers keep working.

## 5. Claim (phase 1)

`POST /runs/{id}/claim`

```json
{"node_id": "6f0c…", "epoch": 7, "claim_id": "7-1-42", "slot": 1}
```

Under the company lock Core re-checks: the run is QUEUED and is its task's live run; `run.agent == task.assignee`;
`Evaluate` (ignoring `live_run`) is `runnable`; not paused; OpenSpec and approved artifacts as today; node online
and of the current epoch; node has the agent's driver and labels; resource capacity and concurrency caps.

- `200 {"lease": "…", "start_by": "2026-10-14T10:00:30Z", "run_spec": {…}, "brief": {"from": 12, "to": 15}}` — run
  CLAIMED. The RunSpec (with the brief built now) replaces `GET /runs/{id}`. Retrying the same
  `(node_id, epoch, claim_id)` returns the same answer; Core does not order claim ids. The RunSpec's
  `materialize` (not stored) lists the project data the node places in the worktree before the runtime starts:
  `{"openspec_dir": "openspec", "files": [{"path", "sha256", "size"}]}` (core-owned-project-data, see
  [OpenSpec](openspec.md)). The node fetches missing blobs with `GET /runs/{id}/blobs/{sha256}` and
  `X-Factory-Lease: <lease>`; a blob it cannot fetch ends the run `setup_failed` before the runtime starts. It
  uploads the run's log, redacted, with `PUT /runs/{id}/logs?seq=N[&final=1]&redacted=1` and the same header,
  during the run and after its terminal event. Both routes are open to node tokens for the node's own runs.
- `409 {"retry": true, "reason": "capacity|agent_busy|project_busy", "detail": "…"}` — run stays QUEUED; nak with
  delay. `detail` (optional) says what the node lacks: a usable driver, labels, a slot, a resource class.
- `409 {"drop": true, "reason": "stale|duplicate|revoked|not_runnable|old_epoch"}` — ack and drop the command; Core
  has already recorded the run's end (`stale`) where applicable.

## 6. Start (phase 2)

`POST /runs/{id}/start {"lease": "…"}` after the workspace is allocated and the runtime launched (session known) →
RUNNING. Retry-safe. The start window is 60 s and is extended by every heartbeat that lists the run as `claimed` or
`starting` (a first clone or a slow runtime launch takes longer), up to 15 minutes after the claim. A CLAIMED run
not started in its window ends as "not started" (§10). `session_id` keeps arriving in the
`started` event as today.

## 7. Inventory heartbeat

`POST /nodes/{node_id}/heartbeat` every `heartbeat_seconds`:

```json
{"epoch": 7,
 "inventory": [{"run_id": "…", "state": "claimed|starting|running|ended_unacked", "elapsed_ms": 81234}],
 "health": {"load1": 2.1, "cpus": 10, "free_disk_bytes": 123456789, "busy_slots": 2, "disk": "ok|low|critical",
            "disk_low_bytes": 10737418240, "disk_critical_bytes": 2147483648,
            "checks": [{"check": "codex.auth", "status": "fail", "detail": "…", "hint": "…"}], "stalled_seconds": 420},
 "drivers": [{"name": "codex", "version": "codex-cli 0.162.1", "ok": true}],
 "hooks": "ios", "hooks_hash": "…"}
```

- `drivers` (optional; the node sends it in every heartbeat) replaces the node's driver list as in register: a
  driver that failed or passed its checks again is declared without a new epoch, and claims use it from then on.
- `health.checks` lists the node's failing self-checks (`git`, `git.user`, `forge.auth`, `ps`, `stack.<name>` from
  the `NodeHooks` checks); `[]` when none fails, absent when the node does not report checks (Core keeps the last
  known ones). Each check that starts failing is one `node.check_failed{check}` event. Driver probes are not in
  it: a failing driver is reported only through `drivers` (its `check` and `detail`), one `driver_unsupported`
  event (§11), so one failure raises one alert.
- `health.stalled_seconds` (optional): no heartbeat succeeded for that long (more than 5 minutes); the node sends
  it until a heartbeat is delivered, which is one `node.heartbeat_stalled` event.
- `hooks`/`hooks_hash`: the `NodeHooks` document the node uses and the hash it applied; when the catalog's document
  hashes differently, the answer carries `hooks` again.

Renews the leases of every listed run (the per-run heartbeat is not used for v2 runs). `elapsed_ms` is measured on
the sleep-excluding clock. A run stays listed until Core acknowledged its end (its terminal event was applied).

Answer `200`:

```json
{"incarnation": "…", "stop": [{"run_id": "…", "reason": "revoked|terminal|timed_out|unknown_run"}],
 "acked": ["run-id"], "cleanup": ["task-id"], "pause": false, "reset": ["task-id"],
 "hooks": {"name": "ios", "hash": "…", "spec": {…}}}
```

`hooks` is present only when the node's `hooks_hash` differs from the catalog document's hash.

- `acked` lists the `ended_unacked` runs whose terminal event Core has applied; the node drops them from its
  inventory. A JetStream publish acknowledgement does not mean Core applied the event.

- `stop` lists every inventory run the node must end: revoked (owner cancel or kill switch), already terminal in
  Core, past `timeout + grace` (Core ends it `timed_out`), unknown to Core (e.g. after a restore).
- Core marks `lost` only RUNNING runs of this node missing from the inventory; CLAIMED runs are judged by
  `start_by`.
- `pause: true` — stop claiming (owner pause of the node, or `health.disk: critical`). The node also pauses on its
  own thresholds from `node.yaml`; both are the same thresholds.
- A changed `incarnation` → the node stops and seals all runs, re-registers with a new epoch, reports nothing older.
- A node silent for 60 s of continuous Core ticking is offline; its unended runs are lost (§10). Limitation: the
  continuity guard protects a laptop that sleeps together with Core on the same machine; with Core on another host,
  a sleeping laptop loses its runs.

## 8. Events

Envelope and lease unchanged; `usage`, `quota` and `POST /runs/{id}/workspace` as already agreed (see
runtime-protocol.md). A run's `status` is its phase: `QUEUED → CLAIMED → RUNNING → (CANCELLING) → ENDED`
(reliable-recovery D1); how it ended is only its `outcome`.

The terminal event carries `{"status": "ENDED", "outcome": "…"}`, and only it carries an outcome. A worker reports
`succeeded`, `failed`, `timed_out`, `idle_timeout`, `max_turns`, `setup_failed`, `paused_out`, `revoked` (Core's stop,
with the runtime's evidence of the interrupt) or `lost`: the run ended **without terminal evidence** (app-server
disconnect, shim exit without a result, an interrupt whose result never came, a node shutdown or lost heartbeat
mid-run), so nothing is known about what the runtime did. Core validates the outcome (anything else, or another
status, is `400 invalid event`) and records it: `lost` is no attempt, with backoff and budget like Core's own losses;
a timeout with a known cause (the executor's timeout, Core's `timed_out`/`idle_timeout` stop) stays `timed_out` /
`idle_timeout` even without evidence, and Core records `idle_timeout` for a `revoked` or `lost` end of a run it
stopped for inactivity. An accepted exit makes the end `succeeded` whatever the worker reported. `no_exit`, `stale`
and `unclaimed` are Core's only.

## 9. Who does what

| Core (stream A) | Node (stream B) |
|---|---|
| `nodes` table (migration), register, epoch rules, incarnation | `<state_dir>/epoch`, register on start, re-register on incarnation change |
| claim re-check, capacity, RunSpec + brief in the claim answer, `start`, start window | slot claim → allocate → launch → `start`; nak/ack on `retry`/`drop` |
| inventory heartbeat: lease renewal, `stop`/`cleanup`/`pause`, offline detection | inventory and health reporting, acting on `stop`/`cleanup`/`pause` |
| pointer commands (with full RunSpec during the transition) | read only `run_id` from commands in v2 |
| v1 endpoints kept until all nodes are v2 | drop per-run heartbeat for v2 runs |

## 10. Decisions from the review

1. **node_id** is generated once and stored in `<state_dir>/node_id`; `name` is a label (macOS renames hosts and two
   machines may share a name). Core accepts duplicate names with a warning.
2. **Lost runs:** a CLAIMED run that missed its start window ends `lost` with result `lost: not started` (the
   runtime never ran, so a retry is safe). A RUNNING run missing from the inventory, runs of an older epoch and runs
   of an offline node end `lost` too (reliable-recovery); the offline node's runs stay unreleased until the node
   proves their runtime gone.
3. **claim_id** is only an idempotency key; claims of parallel slots are not ordered.
4. **Timings:** heartbeat 10 s, `timed_out` grace 60 s, start window 60 s extended by `claimed`/`starting`
   inventory up to 15 min, offline after 60 s of continuous ticking (limitation above).
5. **Control subject** waits for company subject prefixes; until then stops arrive in the heartbeat answer
   (≤ 10 s).
6. **Resources:** node capacity in `node.yaml` `resources`, sent in register; agent needs in `spec.resources`
   (stream B); enforced with claim v2, shortage answers `409 retry/capacity`, never `drop`.
7. **Acknowledged ends:** `acked` in the heartbeat answer (§7).
8. **No mixing:** a registered node uses only v2.

## 11. Core implementation notes

What Core does where the sections above leave room (`internal/storage/nodes.go`, `internal/core/nodes.go`):

- **CLAIMED** is stored (`run_started = 0`, `protocol = 2`, `node_id`, `epoch`, `claim_id`, `slot`, `start_by` on
  the run); `/start` moves it to `RUNNING`. A claimed run holds its agent, project and node capacity like a running
  one, and every live-run rule (cancel, kill switch, writer indexes) applies to it. A v1 claim has no start phase: it
  moves the run to `RUNNING` at once.
- **Leases** of v2 runs last 60 s (the offline threshold) and are renewed only by the inventory heartbeat;
  the per-run `POST /runs/{id}/heartbeat` refuses v2 runs with 409. Lease expiry keeps its continuity guard,
  so a sleeping Core host expires nothing until one window of continuous ticking.
- **Claim** without a body is v1 (the same answer and refusals, without node checks or `start_by`); with
  `{node_id, epoch, claim_id, slot}` it is v2. Checks in
  order: node registered with this epoch (else `drop/old_epoch`); same `(node_id, epoch, claim_id)` → the
  same answer; revoked → `drop/revoked`; claimed by another claim → `drop/duplicate`; not QUEUED →
  `drop/stale`; task's live run is another run, assignee changed, cancel requested, or the task's `why`
  verdict (the run itself ignored) is terminal, backlog, unassigned, parked or waiting on dependencies →
  `drop/not_runnable` and the run ends `stale`;
  paused (company, project, agent, quota key) → `retry/paused`; no `ok` driver for the provider or a missing
  agent label → `retry/capacity`; the agent has another live run → `retry/agent_busy`; no free slot or
  resource classes short (`spec.resources` of the run's agent snapshot against node.yaml `resources` minus the
  needs of the runs the node holds, see "Capacity until the seal"; an undeclared class has capacity 0) →
  `retry/capacity`; OpenSpec or approved-artifact
  checks fail (recorded on the task as in v1) → `drop/not_runnable`. Refusals are JSON:
  `409 {"retry":true,"reason":…}` / `409 {"drop":true,"reason":…}`.
- **Claim answer**: `run_spec` is the stored RunSpec plus `company`, its prompt the brief built in the claim's
  transaction; `brief` is `{from, to}` of the run's trigger window; `start_by` is claim + 60 s. Branch names stay the node's (`af/<task>…`).
- **Register** answers `409 {"reason":"stale_epoch"}` for an epoch not above the last one. **Heartbeat**
  answers `409 {"reason":"stale_epoch"}` for an older epoch and `404 {"reason":"unknown_node"}` for an
  unknown node or a newer epoch than registered (the node registers again).
- **Heartbeat effects**: listed unended runs get their lease renewed; `claimed`/`starting` entries of an
  unstarted run extend `start_by` to now + 60 s, never past claim + 15 min. `stop`: `revoked` (CANCELLING or
  kill switch), `terminal` (Core already ended it), `unknown_run` (not this node's epoch), `timed_out`
  (started longer ago than `timeout_seconds` + 60 s; Core ends it `timed_out` and keeps it unreleased
  until the node confirms the stop, since the runtime may still act). `acked`: `ended_unacked` runs whose terminal event Core applied (the run
  records `end_applied`). Started runs of this epoch missing from the inventory for more than 30 s after
  `/start` end `lost` (`lost: missing from node … inventory`). `pause` is true while `health.disk` is
  `critical` (a heartbeat without `health.disk` keeps the last answer). `stop` reason `idle_timeout`: the run had no applied event for 30 minutes (runtime-protocol.md,
  Idle stop). `timed_out` is judged on the entry's `elapsed_ms` (the node's sleep-excluding clock since the
  claim), never on Core's wall clock. `cleanup` lists closed tasks (DONE/CANCELLED) with a worktree reported by this node, each in at
  most 30 answers (the node retries a busy worktree from the next listing).
- **Lost runs** (§10.2): a new epoch, an offline node (no heartbeat or claim for 60 s of continuous ticking)
  or an expired lease end the node's unstarted runs `lost` (`lost: not started (…)`), and started runs `lost`
  (`lost: …`; released for a new epoch, whose node killed the old process groups
  before registering; unreleased for an offline node until it proves them gone). A node's terminal event that
  arrives later still replaces `lost`, as in v1; a late `revoked` or `lost` end only releases the run (its runtime
  is gone, nothing more is known). A heartbeat from an offline node brings it back online.
- **Incarnation** lives in the database (`core_meta`) with a copy in Core's data directory (`core.incarnation`);
  when the two differ at start (a restored database), Core creates a new incarnation. Restoring the file together
  with the database is not detected: after such a restore write any other value into `core.incarnation` (for
  example `echo restored > data/core.incarnation`) before starting Core. Runbook: [development](development.md#restoring-cores-database).
- **Project setup** (project-creation): a claim answer whose RunSpec carries `project_setup` `{mode, name, description, workspace, repo_url, default_branch, openspec_dir}` (and no workspace) asks the node to create or onboard the project's repository before the runtime starts; the run holds the writer slot `project-setup/<project>`. The node reports `POST /runs/{id}/project-setup` `{lease, node_id, workspace, repo_url, default_branch, openspec: [{path, content}], openspec_untracked}` or `{lease, node_id, error}` (node tokens may call it for their own runs) and continues with the answer `{workspace, repo_url, default_branch, workspace_mode, materialize}`; an error ends the run `setup_failed`. See [projects](projects.md).
- **Worktrees** (additive register field `worktrees: true`, node.yaml `worktrees.enabled`): a node that runs
  worktree tasks in their own worktree lets tasks of one project run in parallel. A worktree run holds its own
  writer slot (`runs.workspace = "<project workspace>#task/<task>"`); claimed by a node without worktrees (or by
  a v1 worker) it holds the project workspace instead, and a collision answers `409 retry/project_busy`.
- `GET /nodes` lists registered nodes (epoch, drivers, resources, health, `online`, `last_seen`, and `in_use`
  `{runs, resources}`: what the current epoch's unended runs hold); `factory status` prints them as its node section.
- **Node tokens** (node-agent 1.1): the owner issues them (`POST /node-tokens {name, node_id?}` → `{id, token}`,
  shown once; `GET /node-tokens`; `POST /node-tokens/{id}/revoke`; CLI `factory nodes token create|list|revoke`).
  Core stores only the SHA-256. A token issued without `node_id` is bound by its first register. A node token
  reaches only `POST /nodes/register` (its `node_id`), `POST /nodes/{its id}/heartbeat`, v2 `POST /runs/{id}/claim`
  naming its node, `POST /runs/{id}/start|workspace|credential|heartbeat` of runs its node claimed, and
  `GET /health`; anything else is `403`, an unknown or revoked token `401`. `DELETE /nodes/{id}` (`factory nodes
  remove`) forgets an offline node: its unended runs are lost, its tokens revoked.
- **Labels**: agent `spec.labels` must all be in the node's register `labels`, else `retry/capacity` with
  `detail: "node lacks labels …"`.
- **Capacity until the seal**: a run holds its slot and resource classes from the claim while it is unended in
  Core **or** the node's last inventory listed it `claimed`, `starting` or `running` (`runs.node_held`); the node
  lists a run `ended_unacked` only after its seal and run-stop hooks, so an ended but unsealed run still holds its
  class. A new epoch releases what the older process held.
- **Drivers**: register and heartbeat `drivers` update the node's drivers. Each change of a driver between usable
  and not usable is one feed event: `driver_unsupported` (attention; summary names the driver, the node, the
  failing `check` and `detail`) or `driver_restored`. A driver not seen before counts as usable, so a first
  register with a failing driver reports it; a new epoch declaring the same failure does not.
- **Offline and continuity**: node silence and v2 leases are judged only over 60 s of continuous Core ticking (v1
  leases keep their 45 s window): after a Core host sleep, a node and its runs get a full 60 s from the wake.
- **Evaluate** (`GET /tasks/{id}/why`, `factory tasks why`) with registered nodes: `pinned_node_unavailable` (the
  node holding the task's unpushed work was removed, is offline past 30 min, or cannot run the assignee; the
  stalled sweep opens one escalation answered by `reset_workspace`, which unpins and makes the next run start a new
  session), `no_capable_node` (no online node without disk pause has the assignee's driver, labels and resources),
  `capacity` (capable nodes are full, or the pinned node is offline for less than the pin timeout). A QUEUED run
  nobody can claim reports these instead of `live_run`.
- **Why a run is QUEUED** (`queued` on the run in `GET /status`, `GET /runs/{id}` and `GET /tasks/{id}/why`;
  `storage.QueuedWhy`): the claim's own checks judged without a claim — a pause (`paused`, with the owner's reason
  and a quota pause's `until`), the agent's cap (`agent_busy`), the pinned node, else each node's answer
  (`node_offline`, `node_paused` by the owner or at critical disk, `driver_disabled` with the hold-off `until`,
  `labels`, `no_slot`, `resource_busy`) in `nodes`, the node closest to taking the run giving `reason` and
  `detail`; `unclaimed` when a node has room, `no_node` without registered nodes.
- **Node health events** (node-agent D5; company-wide feed rows, entity `node:<id>`, written in the transaction of
  the register, heartbeat or expiry that saw the change; the episode state is in the `nodes` row):
  `node.registered` (every register), `node.online` (a node that was offline is seen again), `node.offline` (silent
  for 60 s; again with attention after 10 more minutes, once), `node.low_disk` (attention; `health.disk` low or
  critical, once until it is `ok` again, then `node.disk_ok`), `node.disk_critical` (attention; once per episode),
  `node.paused`/`node.resumed` (Core's `pause` answer changes), `node.restart_loop` (attention; 4 process restarts
  — registers with reason `start` after the node's first — within 15 minutes, once until a window without a
  restart), `node.check_failed` (attention, `ref` = the check; once per failure, `node.check_passed` when it passes
  again, kept across restarts), `node.heartbeat_stalled` (attention; from `health.stalled_seconds`).

Not yet: a per-node owner pause.
