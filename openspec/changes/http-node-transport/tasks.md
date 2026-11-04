Planned (owner decision 2026-11-04: remove NATS; takes the postponed decision in `docs/backlog.md`). Not started.
No migration (not released). Must land before the first external node. Archive `node-identity` first: this
change's `node-agent` delta builds on its version of "One node process with worker slots".

Order: Core routes next to NATS, node on HTTP, then removal; each step keeps `make check` green.

## 1. Decide
- [x] 1.1 Owner decides to remove NATS and use signed HTTPS with long polling (D1).

## 2. Specs and docs
- [x] 2.1 Delta specs `node-transport`, `authority-input`, `event-feed`, `node-agent`, `postgres-tenancy`,
  `trigger-scheduling`, `foundation`, `task-engine`, `messaging`; `openspec validate --strict`.
- [ ] 2.2 Docs: `runtime-protocol-v2.md` (poll, offers, events route, spool; command and control subject gone),
  `architecture.md`, `development.md` (no NATS service, node.yaml without `nats`), `backlog.md` (known issue closed).

## 3. Core
- [ ] 3.1 Offers query (the claim re-check's predicate, oldest first, free slots per provider), index for `QUEUED`.
- [ ] 3.2 Notifier keyed by company and node; signals after commits of queued runs, stops, resets, cleanups, hooks
  changes and sync jobs.
- [ ] 3.3 `POST /nodes/{node_id}/poll` in the node route class (D2): one outstanding poll per node, wait ≤ 25 s,
  2 s shared re-check per company, revocation ends a waiting poll. Verify: answer on signal, empty after the wait,
  older poll ended, paused node gets no offers, other company's runs never offered.
- [ ] 3.4 `POST /runs/{run}/events` (D5): run's node and lease, ordered by `seq`, idempotent by id, gap refused,
  batch limits. Verify: duplicate batch, gap, other node's run 404, lease mismatch.

## 4. Node
- [ ] 4.1 Poll loop replaces the JetStream fetch: offers → existing claim, stops/resets/cleanups/hooks/sync jobs
  handled as from the heartbeat answer; backoff with jitter; `wait_seconds` in node.yaml.
- [ ] 4.2 Event spool `<state_dir>/runs/<run>/events.jsonl` and ordered sender; resume at start; spool removed after
  the terminal event is applied. Verify: Core down during a run, node restart with an unsent spool.

## 5. Removal
- [ ] 5.1 Remove the outbox (table, relay, `RedeliverQueued` republishing), the Core consumer of `FACTORY_EVENTS`,
  the control subject publish (`internal/storage/exits.go`), `internal/bus`, protocol subjects.
- [ ] 5.2 Remove NATS from `compose.yaml`, `.env.example`, node.yaml (`nats`), self-checks and doctor,
  `scripts/nats-secure-test.sh`; rework JetStream tests (`TestJetStream*`, including the one named in
  `core-secret-store` 3.2) on the HTTP transport; drop the `-p 1` NATS rule from docs and `make` targets.

## 6. Verification
- [ ] 6.1 `make check` green; end-to-end with a real `factory-node`: run offered, claimed, events applied, urgent stop
  through a waiting poll, Core restart during a run with the spool delivered, two nodes racing one offer, a node of
  another company gets nothing.
