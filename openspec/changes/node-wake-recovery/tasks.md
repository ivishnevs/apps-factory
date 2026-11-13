## 1. Event spool keeps runs on auth refusals

- [ ] 1.1 Node: 401/403 on `POST /runs/{run}/events` retry with backoff and keep the spool; drop only on refusals that
      can never apply (another node's or unknown run, invalid events); tests with a fake Core answering 401 then 200.
- [ ] 1.2 Node: a spool older than the retry horizon raises a node health event; test.

## 2. Ended runs release lost runs

- [ ] 2.1 Core heartbeat: a listed `ended_unacked` run that is lost and unreleased is released and acked; its task
      retries after the backoff; tests (fixture: lost unreleased run, node lists it ended, terminal event never sent).
- [ ] 2.2 Late terminal evidence after the release still replaces the outcome; test.

## 3. Clock skew after wake

- [ ] 3.1 Core: a clock-window refusal carries Core's time and a reason code; test.
- [ ] 3.2 Node: learn the offset from the refusal, sign with the corrected time (bounded ±1 h), retry at once, report the
      offset in the heartbeat; tests with a fake clock 5 min behind.
- [ ] 3.3 Core: `node.clock_skew` health event once per episode; Nodes page shows the skew.
- [ ] 3.4 Core liveness: clock-refused heartbeats keep the node online for at most one more TTL; test (wake replay: no
      run lost when the corrected heartbeat lands within the TTL).

## 4. Docs

- [ ] 4.1 Node troubleshooting doc: sleeping hosts, clock skew, what the owner sees.
