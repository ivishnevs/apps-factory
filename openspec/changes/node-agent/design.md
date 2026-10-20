## Context
`cmd/codex-worker` subscribes with `MaxAckPending(1)`, claims one run, executes it with per-run heartbeats (`POST /runs/{id}/heartbeat`, 10 s, lease 45 s) and exits nothing else. `reliable-recovery` added worker id, epoch and two-phase claim; `task-worktrees` added seal and node pinning metadata.

## Goals / Non-Goals
Goals: a machine is one schedulable unit with explicit capacity; overload, full disks and host UI interference are prevented by the system, not by prompts; Core knows each machine's health.
Non-goals: autoscaling, heterogeneous pools across companies, sandboxing (shims are a guardrail, not a security boundary — agents run as the same OS user).

## Decisions

### D1. Node process and configuration
`cmd/factory-node` reads `node.yaml`: `node_id`, `slots` (default 2), `resources` (`{simulator: 2, build: 2}`), `labels` (`[macos, xcode]`), `workspace_root`, `disk.low_watermark`, `disk.critical_watermark`, `hooks` (name of a `NodeHooks` document from the company catalog). It authenticates to Core with a **node token** (per machine, company-scoped) and connects to NATS **outbound** over TLS or WebSocket with its own NATS credentials (a per-node user whose permissions cover only its company's command subjects and its own control subject); no inbound port is needed on the machine. NATS leaf nodes and per-company accounts come later. Each slot is a goroutine running the existing executor (`internal/worker.execute`, generalized by `runtime-drivers`). `cmd/codex-worker` remains for one release as `factory-node --slots 1`; the standalone Claude service (`workers/claude`) remains until `runtime-drivers` runs Claude as a driver inside the node.

### D2. Registration and inventory heartbeat
- Start: seal worktrees of runs from the previous epoch, then `POST /nodes/register {node_id, epoch, slots, resources, labels, drivers, protocol_version}`. A new epoch ends the previous epoch's unended runs as `lost` (not budget-counted).
- Every 10 s: `POST /nodes/{id}/heartbeat {epoch, inventory: [{run_id, state: claimed|starting|running|ended_unacked, elapsed}], health: {load1, cpus, free_disk, busy_slots}}`. It renews the leases of all listed runs (per-run heartbeats are removed). `elapsed` uses the sleep-excluding clock. A run stays listed until Core acknowledges its end report, so crash loops are counted.
- Core answer: `{stop: [run_ids], cleanup: [task_ids], pause: bool}`. `stop` lists every inventory run the node must end: revoked (CANCELLING), already terminal in Core, timed out (`elapsed` > agent timeout + grace, ended `timed_out`), and run ids Core does not know (e.g. after a database restore). Core marks `lost` only RUNNING runs missing from the inventory (CLAIMED runs are judged by the start window); lists closed tasks with worktrees on this node under `cleanup`.
- **Server incarnation:** Core stores an `incarnation` id, created with the database and replaced when a database is restored from a backup (restore runbook step). Register and heartbeat answers carry it; a node that sees a new incarnation stops and seals all its runs, re-registers with a new epoch, and reports nothing from before. Runs that had no accepted exit at the backup point are lost; their tasks run again from the triggers in the restored database.
- Offline: a node silent for 60 s of continuous Core ticking (continuity guard from `reliable-recovery`) is `offline`; its unended runs become `lost`. Back online on the next heartbeat.

### D3. Commands and claim with capacity
A slot fetches from `factory.<company>.commands.run.<provider>` only while it is free and only for drivers the node has. The node also subscribes to its control subject `factory.<company>.nodes.<node_id>.control` (plain fan-out, outside the work-queue stream) for `run.stop` (`explicit-exits-authority` D7), `cleanup` and `pause`; the heartbeat answer remains the guarantee. Agents declare `spec.labels` and `spec.resources` (e.g. `{simulator: 1}`). Claim (under the company lock) additionally checks: node online and not paused; node has the agent's labels and driver; busy slots < slots; for each class, sum of needs of the node's live-or-unsealed runs + this run's needs ≤ capacity; task pinning (`task-worktrees` D8). Capacity refusal answers `409 {"retry": true}` (the node naks with delay; the run stays QUEUED); other refusals `{"drop": true}` as before. Capacity is released when the run has ended **and** is sealed. `Evaluate` reasons: `pinned_node_unavailable` = the task's pinned node is offline beyond `pin_timeout`, removed, or lacks the assignee's labels/driver (`task-worktrees` D8); `no_capable_node` = no online, unpaused node of the company has the labels/driver; `capacity` = capable nodes are full.

### D4. Janitor
`NodeHooks` (company catalog, template data): `on_task_closed` and `on_run_stop` commands (env `FACTORY_TASK_ID`, `FACTORY_RUN_ID`, `FACTORY_WORKTREE`; e.g. delete simulators named/tagged with the task id), `prune` entries `{command, older_than}` (e.g. DerivedData, Gradle caches). The janitor runs on every `cleanup` list and every 10 minutes: for closed tasks — seal if needed, `on_task_closed` hooks, `git worktree remove`; then `prune`. Hooks are trusted template data and run on the node with a timeout. Below `low_watermark`: prune with `older_than = 0` for closed tasks and emit one `node.low_disk` attention event (re-armed after recovery). Below `critical_watermark`: the node reports `pause` (stops claiming) and emits `node.disk_critical`.

### D5. Node health events
Core emits `node.registered`, `node.offline`, `node.online`, `node.low_disk`, `node.disk_critical`, `node.paused`, `node.restart_loop`, `node.check_failed{check}`, `node.heartbeat_stalled` into the event feed (`feed_events`, `trigger-log-scheduling`). Attention: `node.offline` longer than 10 min, `node.low_disk`, `node.disk_critical`, `node.restart_loop`, `node.check_failed`, `node.heartbeat_stalled`; `factory status` shows per node slots, busy slots, resources in use, load and free disk.

### D6. Command-level guards from policies
Access rules gain `commands: [glob patterns]` (matched against argv[0] basename and the full command line) with the usual `effect: deny|approval|allow`. A mandatory `host-ui` policy in the default catalog denies host-input tools (`cliclick`, `osascript`, `xdotool`, AppleScript/JXA runners) with the instruction to use debug launch arguments, deep links or UI tests by accessibility identifier. A mandatory `owner-cli` policy denies the `factory` owner CLI with the instruction to use Factory MCP tools (`explicit-exits-authority` D9). The node materializes, per run, a bin directory placed **first on PATH** with refusing shims for denied commands (exit 126 with the rule's instruction; the refusal is reported as a run event) and checking shims for approval commands: the shim asks Core whether an approved single-use grant exists for the exact argv (via the run token; existing action approvals bound to exact arguments) and passes through once, otherwise tells the agent to request approval with `factory_owner_request_approval`. Driver-level deny hooks are added by `runtime-drivers`. This is a guardrail: an agent can bypass it with an absolute path.

### D7. Node health control
- **Supervision:** the node runs under launchd (macOS) / systemd (Linux) with KeepAlive/Restart=always. Every restart
  registers a new epoch; Core counts epochs per node and emits one `node.restart_loop` alert when more than 3 occur
  within 15 minutes.
- **Internal watchdog** (goroutine, every 30 s): every busy slot has a live process group and every agent process
  belongs to a slot (mismatches → stop the stray group, seal, report the run's outcome); orphans with `FACTORY_RUN_ID`
  of this node and no slot are killed; the last successful heartbeat is younger than 3 intervals (otherwise log,
  reconnect, and after 5 minutes report `node.heartbeat_stalled` once connectivity returns).
- **Self-checks** (on start and every 10 minutes): git and forge credentials work (`git ls-remote` on a configured
  repo, forge API token check); Core and NATS are reachable; free disk above the low watermark; stack checks from
  template hooks (e.g. `xcrun simctl list` works, Xcode version). Runtime checks (binary, supported version,
  authentication) are **driver probes**: this change defines the hook interface `node.RuntimeProbe{Probe(ctx) []CheckResult}` and a Codex
  probe; `runtime-drivers` makes every `Driver` implement it, adds the Claude probe and owns the semantics (a failing probe disables that
  driver on the node with `driver_unsupported`). A failed node check emits one `node.check_failed{check}` event. The
  node sets itself `paused` (Core stops granting claims) only when it cannot work at all: no driver remains usable, no
  git/forge credentials, or critical disk; the pause clears automatically when the check passes again.
- **`factory node doctor`** runs every self-check locally and prints a pass/fail table with remediation hints; it works
  without Core.

## Risks / Trade-offs
- One process is a single point of failure per machine → slots are isolated goroutines with their own process groups; a node crash is a new epoch with sealing on restart.
- Resource needs declared per agent, not per run → conservative defaults; mid-run requests are out of scope.
- Hooks are arbitrary commands → they are company template data, reviewed like code; timeouts and logs per hook.

## Amendments

- 2026-10-20: node.yaml paths (`state_dir`, `bin_dir`, `skills_root`, `disk.path`, `worktrees.root`, `token_file`, `drivers.claude.shim`, NATS creds/seed/TLS files, a driver binary or executable holding a `/`) resolve to absolute at config load against the working directory the node starts in (what the `state_dir: data/...` examples expect), not the config file directory. A relative state_dir broke conversation runs (`filepath.Rel` against an absolute repository top level).
