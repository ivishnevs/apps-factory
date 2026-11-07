## 1. Limits

- [ ] 1.1 `concurrency` per role in the company config (default developer 2, manager 2, others 1);
      `max_concurrent_runs` overrides; team page shows limit, live runs and queue per agent; tests.

## 2. Signals

- [ ] 2.1 Throughput sweep: per project, free slots vs queued work and the cause (agent limit, affinity behind a paused
      or busy node, no ready node, dependency gates); one notice per episode to the manager after 30 min; tests.
- [ ] 2.2 `planning_gated` warning on create/update when a spec/design/plan task depends on a verification or
      implementation task; gates listed in the notice with the count of tasks behind them.

## 3. Board and instructions

- [ ] 3.1 Blocked header split ("need you" / "waiting their turn"), Bottlenecks strip, queued runs under Running
      with their reason.
- [ ] 3.2 Default manager prompt: planning gates and throughput notices.

## 4. Verify

- [ ] 4.1 Replay 2026-11-07 on a sandbox: the notice names network-dev's limit and the two QA gates within 30 min;
      the seven spec tasks warn `planning_gated` at creation; the board shows the bottlenecks without API reading.
