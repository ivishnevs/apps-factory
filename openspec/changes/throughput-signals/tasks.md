## 1. Sweep

- [x] 1.1 Throughput sweep: per project, queued work vs free slots with the causes taken from the queued reasons
      (`queuedNodes`) and dependency gates (tasks behind each); one notice per episode to the manager and an owner
      alert "with <manager>" after the threshold (default 30 min, company setting); never changes anything; tests
      (agent limit, disk-paused holder, owner-paused nodes, a gate with tasks behind it).
      Done: `storage.SweepThroughput` / `Bottlenecks` (internal/storage/throughput.go), called by the stall sweep.
      Causes: every QUEUED run's `queuedWhy` grouped by reason and its new `subject` (agent, node, pause, project),
      plus the capacity waits of tasks without a run (the agent's limit as seen on 2026-11-07 is a task verdict,
      not a queued run: Dispatch refuses over the cap) mapped to the same reasons, plus gates (an open task with at
      least 3 released tasks waiting on it directly; the summary also counts everything behind it). Capacity-bound
      reasons (no_slot, resource_busy, agent_busy, a tie to an active node) count only while a slot is idle; every
      other reason, including `node_disk_critical` and reasons added later, counts as it is. Episodes live in
      `throughput_causes` (migration 0035); past `throughput_after_minutes` (default 30, `PUT
      /settings/company/stalls`) one task alert kind `throughput` (attention feed item, anchored on the first
      affected task) and one notification from `factory` to the project's lead per sweep with the newly due causes
      and their fixes. `GET /throughput`. Tests: TestThroughputDiskPausedHolder, TestThroughputOwnerPausedNodes,
      TestThroughputAgentLimitAndGate (storage), TestThroughputSweep (core).

## 2. Limits and resources

- [x] 2.1 `concurrency` per role in the company config (default developer 2, manager 2, others 1) for work runs;
      `max_concurrent_runs` overrides; team page shows limit, live runs and queue per agent; tests.
      Done: `company.yaml` `concurrency` (merged over the built-in table), resolved at catalog load for agents
      without `execution.max_concurrent_runs` (`Agent.LimitFromRole`); the team presets' profiles no longer set
      `max_concurrent_runs: 1`. Team page: "N of L running · Q queued" (as before) plus "limit L (role default)" /
      "(set on the agent)". Test: TestRoleConcurrencyDefaults.
- [x] 2.2 Task `resources` added to the agent's at claim; queued reason `resource_busy` names the holder; tests.
      Done: `Task.Resources` (create/update, MCP `resources`), copied into `RunSpec.Resources` at dispatch and
      added to the agent's wherever Core counts resources (claim, node load, queued reasons, evaluate); the
      resource_busy detail ends "held by task T (agent)". Tests: TestTaskResourcesExclusive,
      TestValidateTaskResources.

## 3. Board and instructions

- [x] 3.1 Bottlenecks strip from the sweep, Blocked header split ("need you" / "waiting their turn"), queued runs under
      In progress with their reason.
      Done in web (tasks page: Bottlenecks strip from `GET /throughput` via the overview, Blocked header counts
      and a "Waiting their turn" divider, queued cards In progress with "queued: <reason>") and TUI (⚑ lines over
      the board, Blocked title split, queued cards In progress).
- [x] 3.2 Default manager prompt: planning gates, throughput notices, task resources for exclusive work.
      Done: prompts/manager.md and the game-producer, product-owner and product-manager presets.

## 4. Verify

- [x] 4.1 Replay 2026-11-07 on a sandbox: within 30 min the notice names network-dev's limit and the two QA gates with
      the tasks behind them; the board shows the bottlenecks without API reading.
      Replayed with fakes (TestThroughputSweep: an agent at limit 1 with four waiting tasks and free slots, a gate
      with three tasks behind it; nothing within 30 minutes, then one notice and one alert per cause, no repeats,
      the gate's alert clears when it closes; `GET /throughput` shows them due). The board strip reads the same
      list; the web has no component test runner, so it was checked by type check, lint and build only.
- [x] 4.2 Replay 2026-11-12: a node with sessions goes critical on disk; the notice names it and the release time.
      Replayed with fakes (TestThroughputDiskPausedHolder).
