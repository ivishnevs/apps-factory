# Runtime protocol v2 — DRAFT for review

Status: draft (2026-10-14), not implemented. Consolidates `node-agent` D2–D3, `reliable-recovery` D6–D7 and
`trigger-log-scheduling` D4/D6 into one Core ↔ node contract so both sides can build against it. Nothing here
changes the current (v1) behaviour until a node registers with `protocol_version: 2`.

## 1. Versions and transition

- v1 (today): per-run `GET /runs/{id}` → `POST /runs/{id}/claim` → `POST /runs/{id}/heartbeat`, command payload is
  the full RunSpec.
- v2: a node registers, claims in two phases, renews all its leases with one inventory heartbeat, and treats the
  command as a pointer.
- Core serves v1 and v2 side by side. A run claimed through v2 is renewed only by its node's heartbeat; a v1 run only
  by its per-run heartbeat. v1 is removed one release after every node runs v2.

## 2. Identities

| Name | Meaning | Source |
|---|---|---|
| `node_id` | one per machine and company | `node.yaml` `name` (today's `Config.Name`) |
| `epoch` | increments on every node process start, never reused | integer in `<state_dir>/epoch`, written before register |
| `slot` | slot index 0..slots-1 | node |
| `claim_id` | increments per claim within an epoch; a lower one is refused | node, per epoch |
| `lease` | per-run secret returned by claim | Core |
| `incarnation` | Core database identity; changes when a backup is restored | Core |

Authentication stays the shared worker token until node tokens exist; every request carries `node_id`.

## 3. Register

`POST /nodes/register`

```json
{"node_id": "mini-1", "epoch": 7, "protocol_version": 2, "slots": 3,
 "drivers": [{"name": "codex", "version": "codex-cli 0.162.1", "ok": true}],
 "resources": {"simulator": 2, "build": 2}, "labels": ["macos", "xcode"]}
```

Answer `200 {"incarnation": "…", "heartbeat_seconds": 10, "start_window_seconds": 30}`.

Effects: the node is online; every unended run of an older epoch of this node ends `lost` (not budget-counted; until
the outcome model exists Core records it as UNKNOWN with result `lost: node restarted`). A lower or equal epoch than
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
{"node_id": "mini-1", "epoch": 7, "claim_id": 42, "slot": 1}
```

Under the company lock Core re-checks: the run is QUEUED and is its task's live run; `run.agent == task.assignee`;
`Evaluate` (ignoring `live_run`) is `runnable`; not paused; OpenSpec and approved artifacts as today; node online
and of the current epoch; node has the agent's driver and labels; resource capacity and concurrency caps.

- `200 {"lease": "…", "start_by": "2026-10-14T10:00:30Z", "run_spec": {…}, "brief": {"from": 12, "to": 15}}` — run
  CLAIMED. The RunSpec (with the brief built now) replaces `GET /runs/{id}`. Retrying the same
  `(node_id, epoch, claim_id)` returns the same answer.
- `409 {"retry": true, "reason": "capacity|agent_busy|project_busy"}` — run stays QUEUED; nak with delay.
- `409 {"drop": true, "reason": "stale|duplicate|revoked|not_runnable|old_epoch"}` — ack and drop the command; Core
  has already recorded the run's end (`stale`) where applicable.

## 6. Start (phase 2)

`POST /runs/{id}/start {"lease": "…"}` after the workspace is allocated and the runtime launched (session known) →
RUNNING. Retry-safe. A CLAIMED run not started by `start_by` ends `lost`. `session_id` keeps arriving in the
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
 "cleanup": ["task-id"], "pause": false}
```

- `stop` lists every inventory run the node must end: revoked (owner cancel or kill switch), already terminal in
  Core, past `timeout + grace` (Core ends it `timed_out`), unknown to Core (e.g. after a restore).
- Core marks `lost` only RUNNING runs of this node missing from the inventory; CLAIMED runs are judged by
  `start_by`.
- `pause: true` — stop claiming (owner pause of the node, or node disk critical as reported).
- A changed `incarnation` → the node stops and seals all runs, re-registers with a new epoch, reports nothing older.
- A node silent for 60 s of continuous Core ticking is offline; its unended runs end `lost`.

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

## 10. Open questions for review

1. `node_id` = `node.yaml` `name`, unique per company — or a generated id with the name as a label?
2. `lost` before the outcome model: record as UNKNOWN (quarantines the project like today) or end it FAILED with a
   `lost` result so the task can retry? Depends on the owner's decision on automatic retry.
3. `claim_id`: a per-epoch counter on the node (proposed) or a random id plus Core-side ordering?
4. Timings: heartbeat 10 s, start window 30 s, offline after 60 s, `timed_out` grace 60 s — fine for laptops?
5. Control subject (`factory.<company>.nodes.<node_id>.control`) now, or rely on the heartbeat answer only until
   company subject prefixes land?
6. Resources: node declares capacity in register; who declares per-agent needs (`spec.resources` in agent YAML,
   stream B) and when do we start enforcing them?
