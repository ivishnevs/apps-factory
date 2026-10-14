# Runtime protocol v2

Status: agreed (2026-10-14) after stream B's review; implemented on both sides (Core: §11) and run together in
`TestJetStreamNodeV2RealCore`. Consolidates `node-agent` D2–D3, `reliable-recovery` D6–D7 and
`trigger-log-scheduling` D4/D6 into one Core ↔ node contract so both sides can build against it. Nothing here
changes the current (v1) behaviour until a node registers with `protocol_version: 2`.

## 1. Versions and transition

- v1 (today): per-run `GET /runs/{id}` → `POST /runs/{id}/claim` → `POST /runs/{id}/heartbeat`, command payload is
  the full RunSpec.
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

Authentication stays the shared worker token until node tokens exist; every request carries `node_id`.

## 3. Register

`POST /nodes/register`

```json
{"node_id": "6f0c…", "name": "mini-1", "epoch": 7, "protocol_version": 2, "slots": 3,
 "drivers": [{"name": "codex", "version": "codex-cli 0.162.1", "ok": true}],
 "resources": {"simulator": 2, "build": 2}, "labels": ["macos", "xcode"]}
```

Answer `200 {"incarnation": "…", "heartbeat_seconds": 10, "start_window_seconds": 60}`.

`drivers` come from each driver's `Probe`; `name` is exactly the provider (`codex`, `claude`). `resources` is the
capacity from `node.yaml`.

Effects: the node is online; every unended run of an older epoch of this node is lost (until the outcome model: see
§10 "Lost runs"). A lower or equal epoch than
the last registered one is refused with `409 {"reason": "stale_epoch"}`. Before registering, the node kills and
seals what its previous epoch left (it already kills orphaned groups at start).

## 4. Command

Subject unchanged until company prefixes (`factory.commands.agent.run.<provider>`). Payload becomes a pointer:

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
  `(node_id, epoch, claim_id)` returns the same answer; Core does not order claim ids.
- `409 {"retry": true, "reason": "capacity|agent_busy|project_busy"}` — run stays QUEUED; nak with delay.
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
 "health": {"load1": 2.1, "cpus": 10, "free_disk_bytes": 123456789, "busy_slots": 2, "disk": "ok|low|critical"}}
```

Renews the leases of every listed run (the per-run heartbeat is not used for v2 runs). `elapsed_ms` is measured on
the sleep-excluding clock. A run stays listed until Core acknowledged its end (its terminal event was applied).

Answer `200`:

```json
{"incarnation": "…", "stop": [{"run_id": "…", "reason": "revoked|terminal|timed_out|unknown_run"}],
 "acked": ["run-id"], "cleanup": ["task-id"], "pause": false}
```

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
runtime-protocol.md). Terminal statuses stay COMPLETED/FAILED/CANCELLED/UNKNOWN until the outcome model
(`reliable-recovery` 1.x) adds `outcome`; that change is versioned separately.

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
2. **Lost runs before the outcome model:** a CLAIMED run that missed its start window ends FAILED with result
   `lost: not started` (the runtime never ran, so a retry is safe). A RUNNING run missing from the inventory, runs of
   an older epoch and runs of an offline node end UNKNOWN as today. The outcome model replaces both later.
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

- **CLAIMED** is stored as status `RUNNING` with `run_started = 0` (`protocol = 2`, `node_id`, `epoch`,
  `claim_id`, `slot`, `start_by` on the run). A claimed run therefore holds its agent, project and node capacity
  like a running one, and every existing live-run rule (cancel, kill switch, writer indexes) applies to it.
  Run reads show `RUNNING` until the outcome model adds statuses.
- **Leases** of v2 runs last 60 s (the offline threshold) and are renewed only by the inventory heartbeat;
  the per-run `POST /runs/{id}/heartbeat` refuses v2 runs with 409. Lease expiry keeps its continuity guard,
  so a sleeping Core host expires nothing until one window of continuous ticking.
- **Claim** without a body is v1 (`{lease}`); with `{node_id, epoch, claim_id, slot}` it is v2. Checks in
  order: node registered with this epoch (else `drop/old_epoch`); same `(node_id, epoch, claim_id)` → the
  same answer; revoked → `drop/revoked`; claimed by another claim → `drop/duplicate`; not QUEUED →
  `drop/stale`; task's live run is another run, assignee changed or cancel requested → `drop/not_runnable`;
  paused (company, project, agent, quota key) → `retry/paused`; no `ok` driver for the provider, no free
  slot, or resource classes short (`spec.resources` of the run's agent snapshot against node.yaml
  `resources` minus the needs of the node's unended runs; an undeclared class has capacity 0) →
  `retry/capacity`; the agent has another live run → `retry/agent_busy`; OpenSpec or approved-artifact
  checks fail (recorded on the task as in v1) → `drop/not_runnable`. Refusals are JSON:
  `409 {"retry":true,"reason":…}` / `409 {"drop":true,"reason":…}`.
- **Claim answer**: `run_spec` is the stored RunSpec plus `company`; `brief` is `{from, to}` of the run's
  trigger window; `start_by` is claim + 60 s. Branch names stay the node's (`af/<task>…`).
- **Register** answers `409 {"reason":"stale_epoch"}` for an epoch not above the last one. **Heartbeat**
  answers `409 {"reason":"stale_epoch"}` for an older epoch and `404 {"reason":"unknown_node"}` for an
  unknown node or a newer epoch than registered (the node registers again).
- **Heartbeat effects**: listed unended runs get their lease renewed; `claimed`/`starting` entries of an
  unstarted run extend `start_by` to now + 60 s, never past claim + 15 min. `stop`: `revoked` (CANCELLING or
  kill switch), `terminal` (Core already ended it), `unknown_run` (not this node's epoch), `timed_out`
  (started longer ago than `timeout_seconds` + 60 s; Core ends it UNKNOWN with result `timed_out: …`, since
  the runtime may still have acted). `acked`: `ended_unacked` runs whose terminal event Core applied (the run
  records `end_applied`). Started runs of this epoch missing from the inventory for more than 30 s after
  `/start` end UNKNOWN (`lost: missing from node … inventory`). `pause` is true while `health.disk` is
  `critical`. `stop` reason `idle_timeout`: the run had no applied event for 30 minutes (runtime-protocol.md,
  Idle stop). `timed_out` is judged on the entry's `elapsed_ms` (the node's sleep-excluding clock since the
  claim), never on Core's wall clock. `cleanup` lists closed tasks (DONE/CANCELLED) with a worktree reported by this node, each in at
  most 30 answers (the node retries a busy worktree from the next listing).
- **Lost runs** (§10.2): a new epoch, an offline node (no heartbeat or claim for 60 s of continuous ticking)
  or an expired lease end the node's unstarted runs FAILED `lost: not started (…)`, retryable like a known
  failure, and started runs UNKNOWN `lost: …`. A node's terminal event that arrives later still resolves an
  UNKNOWN run, as in v1. A heartbeat from an offline node brings it back online.
- **Incarnation** lives in the database (`core_meta`) with a copy in `<db>.incarnation`; when the two differ
  at start (a restored database), Core creates a new incarnation. Restoring the file together with the
  database is not detected: after such a restore write any other value into `<db>.incarnation` (for example
`echo restored > data/factory.db.incarnation`) before starting Core.
- `GET /nodes` lists registered nodes (epoch, drivers, resources, health, `online`, `last_seen`).

Not yet: agent `labels` matching node labels, a per-node owner pause, node health events, declaring driver
changes without a new epoch (a `drivers` field in the heartbeat is the proposed addition).
