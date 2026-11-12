## 1. Sweep

- [ ] 1.1 Throughput sweep: per project, queued work vs free slots with the causes taken from the queued reasons
      (`queuedNodes`) and dependency gates (tasks behind each); one notice per episode to the manager and an owner
      alert "with <manager>" after the threshold (default 30 min, company setting); never changes anything; tests
      (agent limit, disk-paused holder, owner-paused nodes, a gate with tasks behind it).

## 2. Limits and resources

- [ ] 2.1 `concurrency` per role in the company config (default developer 2, manager 2, others 1) for work runs;
      `max_concurrent_runs` overrides; team page shows limit, live runs and queue per agent; tests.
- [ ] 2.2 Task `resources` added to the agent's at claim; queued reason `resource_busy` names the holder; tests.

## 3. Board and instructions

- [ ] 3.1 Bottlenecks strip from the sweep, Blocked header split ("need you" / "waiting their turn"), queued runs under
      In progress with their reason.
- [ ] 3.2 Default manager prompt: planning gates, throughput notices, task resources for exclusive work.

## 4. Verify

- [ ] 4.1 Replay 2026-11-07 on a sandbox: within 30 min the notice names network-dev's limit and the two QA gates with
      the tasks behind them; the board shows the bottlenecks without API reading.
- [ ] 4.2 Replay 2026-11-12: a node with sessions goes critical on disk; the notice names it and the release time.
