## 1. One delivery rule for both node buffers

- [ ] 1.1 Core: machine-readable reasons on every 401/403/410 of node and run-credential routes (`signature_window`,
      `lease_changed`, `node_key_revoked`, `agent_unavailable`, `not_current_run`, ...); tests.
- [ ] 1.2 One refusal classifier (retry / final) by reason and status, shared by the event spool, the node's exit
      redelivery and the adapter; table tests.
- [ ] 1.3 Event spool: keep and retry on retry-class refusals, drop only on final ones; tests with a fake Core answering
      a temporary 401 then 200, and a permanent 401 (lease changed) that is not retried forever.
- [ ] 1.4 Exit delivery (`factorytools/pending.go`, worker redelivery): the same classifier; tests.
- [ ] 1.5 Retry horizon: past it a node health event names the run and what is undelivered; nothing is dropped
      silently; tests.
- [ ] 1.6 Adapter writes the pending file before `POST /agent/exit` and removes it after Core's answer; test killing
      the process group mid-request.
- [ ] 1.7 Redelivery idempotence: an exit and an event batch delivered twice apply once; tests.

## 2. Late exits of lost runs

- [ ] 2.1 Node: on `stop`, stop the runtime, deliver an undelivered exit (exit file or pending file) with
      `context.WithoutCancel` plus a timeout, then send the terminal event; tests for exit-before-stop and
      stop-before-exit.
- [ ] 2.2 Core gateway: `/agent/exit` only, for an ENDED lost or timed_out unreleased run; other tools and the
      credential endpoint still refuse; tests.
- [ ] 2.3 `Store.Exit`: in the same transaction require ENDED, lost or timed_out, `unreleased=1`,
      `task.current_run_id == run`; otherwise 410; tests (accepted; already released: 410; race between release and
      exit; timed_out; conversation run: no exit applied).
- [ ] 2.4 An accepted late exit closes the run (`succeeded`, `unreleased=0`, no retry); `releaseLost` schedules no
      retry for a run with an accepted exit; test (lost unreleased run, exit accepted, task moves on without a retry).

## 3. Ended runs release lost runs

- [ ] 3.1 Core heartbeat: a listed `ended_unacked` run that is lost and unreleased is released and acked; its task
      retries after the backoff unless it has an accepted exit; tests (fixture: lost unreleased run, node lists it
      ended, terminal event never sent).
- [ ] 3.2 Late terminal evidence after the release still replaces the outcome; test.

## 4. Clock skew after wake (owner decisions 1 and 2)

- [ ] 4.1 Core: a clock-window refusal carries Core's time and the `signature_window` reason; test.
- [ ] 4.2 Node: learn the offset from the refusal, sign with the corrected time (bounded ±1 h), retry at once, report
      the offset in the heartbeat; tests with a fake clock 5 min behind.
- [ ] 4.3 Core: `node.clock_skew` health event once per episode; Nodes page shows the skew.
- [ ] 4.4 Core liveness: clock-refused heartbeats keep the node online for at most one more TTL; test (wake replay: no
      run lost when the corrected heartbeat lands within the TTL).

## 5. Docs

- [ ] 5.1 Node troubleshooting and explicit-exits docs: sleeping hosts, clock skew, delivery rule, late exits, what
      the owner sees.
