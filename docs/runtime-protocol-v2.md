# Runtime protocol v2

Status: agreed (2026-10-14) after stream B's review; implemented on both sides (Core: §11). Every
exchange is a signed HTTPS request (`node-identity`, `http-node-transport`): the node polls for work (§4) and sends
run events to `POST /runs/{run}/events` (§8). Consolidates `node-agent` D2–D3, `reliable-recovery` D6–D7 and
`trigger-log-scheduling` D4/D6 into one Core ↔ node contract so both sides can build against it.

## 1. Versions

A node registers, polls for work, claims in two phases and renews all its leases with one inventory heartbeat. This is the only protocol: v1 (a claim without a body, a per-run heartbeat) is removed, and
`protocol_version: 2` remains only as a field of the register payload. [`runtime-protocol.md`](runtime-protocol.md)
is the historical v1 text.

## 2. Identities

| Name | Meaning | Source |
|---|---|---|
| `node_id` | one per machine, unique across companies, stable across renames | generated once, stored in `<state_dir>/node_id` |
| `name` | human label | `node.yaml` `name` (default host name); Core warns when two online nodes share it |
| `epoch` | increments on every node process start, never reused | integer in `<state_dir>/epoch`, written before register |
| `slot` | slot index 0..slots-1 | node |
| `claim_id` | idempotency key of one claim attempt; no ordering (slots claim concurrently) | node, unique within the epoch |
| `lease` | per-run secret returned by claim | Core |
| `incarnation` | Core database identity; changes when a backup is restored | Core |

Nodes have no token: every request is signed with the node's key (RFC 9421 `Signature-Input` / `Signature`, `keyid`
= `node_id`; §11). Every request carries `node_id`.

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

## 4. Poll

`POST /nodes/{node_id}/poll` (signed) is the node's long poll:

```json
{"epoch": 7, "free": {"codex": 2, "claude": 2}, "wait_seconds": 25, "skip": ["run-id"],
 "hooks": "ios", "hooks_hash": "…"}
```

`free` is the node's idle slots per provider whose driver it may claim with (`{}` while it may not claim at all);
`wait_seconds` is at most 25 (node.yaml `poll.wait_seconds`, default 25; lower it behind a proxy that cuts requests
sooner); `skip` lists runs whose claim Core refused with `retry` in the last 5 s. Core answers as soon as there is
something for the node, else empty when the wait ends:

```json
{"offers": [{"run_id": "…", "provider": "codex"}], "stop": [{"run_id": "…", "reason": "revoked"}],
 "reset": ["task-id"], "cleanup": ["task-id"], "hooks_changed": true}
```

- `offers` are hints: `QUEUED` runs of the node's company, oldest first, at most `free` per provider, whose driver is
  usable on the node and whose agent's labels it has, not pinned to another node's unpushed worktree, none while
  the node or the company is paused. Several nodes may get the same offer; only the claim (§5) takes a run, and the
  loser gets `409 drop/duplicate`. The node hands each offer to an idle slot; one it cannot take stays queued.
- `stop`, `reset`, `cleanup` are what the heartbeat answer (§7) lists for the node: stops of its live runs Core
  revoked or gave a stop reason, worktree resets and closed tasks' worktrees. Work a poll of the node already
  answered is not answered again; the heartbeat keeps listing it until it is done. `hooks_changed` asks the node
  for a heartbeat now, whose answer carries the `NodeHooks` document.
- A commit that queues a run, stops or cancels one, resets a worktree, lifts a pause or changes the catalog wakes
  the company's waiting polls; a waiting poll also re-reads every 2 s (a missed wake-up, a second Core process). So an
  urgent stop reaches a waiting node at once; the heartbeat answer stays the guarantee.
- One poll per node: a newer poll ends the older one with an empty answer. An older epoch is `409 stale_epoch`, an
  unknown node or newer epoch `404 unknown_node` (the heartbeat registers again). A waiting poll of a node whose key is
  revoked ends `401`.
- After an error the node polls again with a backoff from 1 s doubling to 30 s, with jitter.

## 5. Claim (phase 1)

`POST /runs/{id}/claim`

```json
{"node_id": "6f0c…", "epoch": 7, "claim_id": "7-1-42", "slot": 1}
```

Under the company lock Core re-checks: the run is QUEUED and is its task's live run; `run.agent == task.assignee`;
`Evaluate` (ignoring `live_run`) is `runnable`; not paused; approved artifacts as today (the OpenSpec change is checked by the node); node online
and of the current epoch; node has the agent's driver and labels; resource capacity and concurrency caps.

- `200 {"lease": "…", "start_by": "2026-10-14T10:00:30Z", "run_spec": {…}, "brief": {"from": 12, "to": 15}}` — run
  CLAIMED. The RunSpec (with the brief built now) replaces `GET /runs/{id}`. Retrying the same
  `(node_id, epoch, claim_id)` returns the same answer; Core does not order claim ids. The RunSpec's
  `materialize` (not stored) lists the project data the node places in the worktree before the runtime starts:
  `{"files": [{"path", "sha256", "size"}]}`, the pinned approved artifact inputs under `.factory/inputs/`
  (core-owned-project-data; OpenSpec is in the checkout, see [OpenSpec](openspec.md)). The node fetches missing blobs with `GET /runs/{id}/blobs/{sha256}` and
  `X-Factory-Lease: <lease>`; a blob it cannot fetch ends the run `setup_failed` before the runtime starts. It
  uploads the run's log, redacted, with `PUT /runs/{id}/logs?seq=N[&final=1]&redacted=1` and the same header,
  during the run and after its terminal event. Both routes are open to a signed node for its own runs.
- The answer's `secrets` (core-secret-store D4, not stored) are the run's company secrets:
  `[{"name", "kind": "string|file", "version", "sealed"}]`, each HPKE-sealed (DHKEM X25519, HKDF-SHA256,
  ChaCha20-Poly1305) to the claiming node's encryption key with info `factory-secret|company|run|node|name|version`;
  only that node opens it, only for that run. Core checks every needed secret before the run is CLAIMED: a missing,
  wrong-kind or unreadable one ends the run `setup_failed` before it starts (no attempt; the task then waits with
  `secret_missing`) and the claim is dropped (`secret_missing`); a Core without `FACTORY_SECRETS_KEY` answers
  `retry` (`no_master_key`) and the run stays QUEUED. A repeated claim seals again; `secret.delivered` is recorded once.
- `409 {"retry": true, "reason": "capacity|agent_busy|project_busy|no_master_key", "detail": "…"}` — run stays QUEUED; the node
  skips the offer for 5 s. `detail` (optional) says what the node lacks: a usable driver, labels, a slot, a resource class.
- `409 {"drop": true, "reason": "stale|duplicate|revoked|not_runnable|old_epoch|secret_missing"}` — the node forgets the offer; Core
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

Renews the leases of every listed run. `elapsed_ms` is measured on
the sleep-excluding clock. A run stays listed until Core acknowledged its end (its terminal event was applied).

Answer `200`:

```json
{"incarnation": "…", "stop": [{"run_id": "…", "reason": "revoked|terminal|timed_out|unknown_run"}],
 "acked": ["run-id"], "cleanup": ["task-id"], "pause": false, "reset": ["task-id"],
 "hooks": {"name": "ios", "hash": "…", "spec": {…}}}
```

`hooks` is present only when the node's `hooks_hash` differs from the catalog document's hash.

- `acked` lists the `ended_unacked` runs whose terminal event Core has applied; the node drops them from its
  inventory. An event spooled or sent is not yet an event Core applied.

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
runtime-protocol.md). The node sends a run's events to `POST /runs/{run}/events` (signed, the run's node):

```json
{"lease": "…", "events": [{"seq": 1, "event": {"event_id": "…", "schema_version": 1, "type": "agent.started", "correlation_id": "run-id", "payload": {…}}}]}
```

`seq` increases by one per run from 1, and every event's payload carries the batch's `lease`. Core applies the
events in order, each with its run update in one transaction, skips sequences it already applied (a batch resent
after a timeout), and answers `{"applied_seq": n}`. A batch starting after the next sequence is
`409 {"reason": "gap", "expected_seq": n}` and the node resends from there; events of another lease are
`409 {"reason": "lease"}` and invalid ones `422`: the node drops them. Another node's run is `403`, an unknown run
`404`. At most 256 events or 1 MiB per batch.

Every event is first appended to the node's spool `<state_dir>/runs/<run>/events.jsonl` (a header line
`{"run", "lease"}`, then one `{"seq", "event"}` per line) and then sent. While Core is unreachable the run goes on
and the node retries with a backoff up to 30 s; a node that restarts resumes the spools it finds. The spool is
removed once Core applied the run's terminal event, and the run leaves the inventory with the next `acked`. A run's `status` is its phase: `QUEUED → CLAIMED → RUNNING → (CANCELLING) → ENDED`
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
| claim re-check, capacity, RunSpec + brief in the claim answer, `start`, start window | slot claim → allocate → launch → `start`; skip on `retry`, forget on `drop` |
| inventory heartbeat: lease renewal, `stop`/`cleanup`/`pause`, offline detection | inventory and health reporting, acting on `stop`/`cleanup`/`pause` |
| poll: offers, stops, resets, cleanups, wake-ups; events route with sequences | poll loop, offers to idle slots, event spool and ordered sender |

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
5. **Stops** reach a waiting poll at once (§4); the heartbeat answer delivers them within 10 s otherwise.
6. **Resources:** node capacity in `node.yaml` `resources`, sent in register; agent needs in `spec.resources`
   (stream B); enforced with claim v2, shortage answers `409 retry/capacity`, never `drop`.
7. **Acknowledged ends:** `acked` in the heartbeat answer (§7).
8. **No mixing:** a registered node uses only v2.

## 11. Core implementation notes

What Core does where the sections above leave room (`internal/storage/nodes.go`, `internal/core/nodes.go`):

- **CLAIMED** is stored (`run_started = 0`, `protocol = 2`, `node_id`, `epoch`, `claim_id`, `slot`, `start_by` on
  the run); `/start` moves it to `RUNNING`. A claimed run holds its agent, project and node capacity like a running
  one, and every live-run rule (cancel, kill switch, writer indexes) applies to it.
- **Leases** of v2 runs last 60 s (the offline threshold) and are renewed only by the inventory heartbeat;
  the per-run `POST /runs/{id}/heartbeat` refuses v2 runs with 409. Lease expiry keeps its continuity guard,
  so a sleeping Core host expires nothing until one window of continuous ticking.
- **Claim** carries `{node_id, epoch, claim_id, slot}`. Checks in
  order: node registered with this epoch (else `drop/old_epoch`); same `(node_id, epoch, claim_id)` → the
  same answer; revoked → `drop/revoked`; claimed by another claim → `drop/duplicate`; not QUEUED →
  `drop/stale`; task's live run is another run, assignee changed, cancel requested, or the task's `why`
  verdict (the run itself ignored) is terminal, backlog, unassigned, parked or waiting on dependencies →
  `drop/not_runnable` and the run ends `stale`;
  paused (company, project, agent, quota key) → `retry/paused`; no `ok` driver for the provider or a missing
  agent label → `retry/capacity`; the agent has another live run → `retry/agent_busy`; no free slot or
  resource classes short (`spec.resources` of the run's agent snapshot against node.yaml `resources` minus the
  needs of the runs the node holds, see "Capacity until the seal"; an undeclared class has capacity 0) →
  `retry/capacity`; approved-artifact
  checks fail (recorded on the task) → `drop/not_runnable`. Refusals are JSON:
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
  arrives later still replaces `lost`; a late `revoked` or `lost` end only releases the run (its runtime
  is gone, nothing more is known). A heartbeat from an offline node brings it back online.
- **Incarnation** lives in the database (`core_meta`) with a copy in Core's data directory (`core.incarnation`);
  when the two differ at start (a restored database), Core creates a new incarnation. Restoring the file together
  with the database is not detected: after such a restore write any other value into `core.incarnation` (for
  example `echo restored > data/core.incarnation`) before starting Core. Runbook: [development](development.md#restoring-cores-database).
- **Project setup** (project-creation): a claim answer whose RunSpec carries `project_setup` `{mode, name, description, workspace, repo_url, default_branch}` (and no workspace) asks the node to create or onboard the project's repository before the runtime starts; the run holds the writer slot `project-setup/<project>`. The node reports `POST /runs/{id}/project-setup` `{lease, node_id, workspace, repo_url, default_branch}` or `{lease, node_id, error}` (a signed node, for its own runs) and continues with the answer `{workspace, repo_url, default_branch, workspace_mode, materialize}`; an error ends the run `setup_failed`. See [projects](projects.md).
- **Worktrees** (additive register field `worktrees: true`, node.yaml `worktrees.enabled`): a node that runs
  worktree tasks in their own worktree lets tasks of one project run in parallel. A worktree run holds its own
  writer slot (`runs.workspace = "<project workspace>#task/<task>"`); claimed by a node without worktrees it holds the project workspace instead, and a collision answers `409 retry/project_busy`.
- `GET /nodes` lists registered nodes (epoch, drivers, resources, health, `online`, `last_seen`, and `in_use`
  `{runs, resources}`: what the current epoch's unended runs hold); `factory status` prints them as its node section.
- **Node identity** (node-identity; runbook: [development](development.md#node-identity)): `POST /node-enrollments
  {name}` (owner or admin; a code valid 15 minutes, single use), `POST /nodes/enroll {code, node_id, sign_pub, box_pub,
  proof}` (no token, rate limited; keys pending, deleted after 24 hours), `GET /node-keys`, `POST
  /node-keys/{id}/approve {fingerprint}`, `POST /node-keys/{id}/reject`. A pending node may only call the signed `GET
  /nodes/{id}/enrollment`. Each node request is signed (`internal/nodesig`): `created` within ±120 s of Core's clock
  and not before Core started, each nonce once (held in Core's memory). A signed node reaches `POST /nodes/register`
  (its `node_id`), `POST /nodes/{its id}/heartbeat|keys`, `POST /runs/{id}/claim` naming its node,
  `POST /runs/{id}/start|workspace|credential|project-setup`, blobs and logs of its runs, and `GET /health`. An
  operator or owner token or session gets `401` on node routes. `POST /nodes/{id}/revoke` deletes the keys and ends
  the node's runs as lost; `POST /nodes/{id}/keys` (signed by the current key) proposes rotated keys, approved like
  new ones. `DELETE /nodes/{id}` (`factory nodes remove`) forgets an offline node: its unended runs are lost, its
  keys deleted.
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
- **Offline and continuity**: node silence and leases are judged only over 60 s of continuous Core ticking: after a Core host sleep, a node and its runs get a full 60 s from the wake.
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
