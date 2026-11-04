## Context

See proposal. Related: `node-identity` (RFC 9421 signed node requests, global node id, node route class,
revocation), `core-secret-store` (sealed secrets in the claim answer, wait before dispatch), `skill-registry`
(`GET /runs/{run}/skills/{hash}`, private source sync jobs for a node), `authority-input` (urgent stop),
`trigger-scheduling` (dispatcher, reconciler, `feed_events`), `node-agent` (heartbeat, slots, resource classes),
`docs/runtime-protocol-v2.md` (register, two-phase claim, inventory heartbeat).

Today Core writes outbox rows in the transaction of their cause and a relay publishes them to NATS: run commands to
`factory.<company>.commands.agent.run.<provider>` (one durable pull consumer per company and provider, max one
unacked), stops to `factory.<company>.nodes.<node>.control`, feed and events to `FACTORY_EVENTS`. Nodes publish run
events to `factory.<company>.events.agent.*`; Core consumes them per company and applies them with the run's lease.
Everything else between node and Core is already signed HTTP.

## Decisions (owner 2026-11-04)

### D1. One transport: signed HTTPS
Every node ↔ Core exchange is a signed node request (`node-identity` D3) to Core's node routes. Company isolation is
the gateway's: a node id belongs to one company, and every route checks that the run, task or job belongs to that
node's company (and, where named, to that node). There is no broker, no second login and no second revocation path.

### D2. Poll
`POST /nodes/{node_id}/poll` with `{epoch, free: {<provider>: <slots>}, wait_seconds}`; `wait_seconds` at most 25
(below proxy read timeouts of 60 s; CapRover's nginx keeps its default). Core answers as soon as the node has
something, else empty at the end of the wait:
```json
{"offers": [{"run_id": "…", "provider": "claude"}],
 "stop": [{"run_id": "…", "reason": "urgent_directive"}], "reset": ["task-…"], "cleanup": ["task-…"],
 "hooks_changed": true, "sync_jobs": [{"id": "…", "source": "…", "ref": "…"}]}
```
One outstanding poll per node: a new poll ends the older one with an empty answer. The node polls again at once
after an answer, with backoff and jitter after errors. The inventory heartbeat stays as it is (leases, liveness,
offline detection, its answer still carries stop/cleanup/reset/pause as the guarantee); the poll is the fast path.
A paused node, or one without a usable driver, gets no offers.

### D3. Offers and claim
Offers are hints, computed per poll: `QUEUED` runs of the node's company whose provider has free slots on the node
and whose resource class, labels and project setup rules the node meets (the same predicate the claim re-check
uses), oldest first, at most the free slots. Several nodes may see the same offer; the existing signed two-phase
claim (`runtime-protocol-v2` §5) stays the only way to take a run and is atomic: the loser gets `409` and polls
again. Secrets (`core-secret-store`) and skills (`skill-registry`) are unchanged: sealed in the claim answer and
downloaded by hash.

### D4. Stops and node actions
An urgent stop, revoke, reset or cleanup is run or task state in Postgres (as the heartbeat answer already reads
it); its commit wakes the node's poll (D6), so a stop reaches a waiting node within milliseconds and otherwise at
the next poll or heartbeat. The control subject and its outbox row go. The node acts on a stop only for runs in its
inventory (unchanged).

### D5. Run events over HTTP
`POST /runs/{run}/events` from the run's node, `{lease, events: [{id, seq, kind, payload}]}`, `seq` increasing per
run from 1. Core applies events in `seq` order through today's apply (lease check, conflict and invalid
semantics), skips ids already applied, refuses a gap (`409 {"expected_seq": n}`) and answers `{"applied_seq": n}`.
Limits: 256 events or 1 MiB per batch.

Node spool: every event is appended to `<state_dir>/runs/<run>/events.jsonl` before sending; the sender sends
from the first unapplied `seq`, deletes the spool after the terminal event is applied, and resumes spools at node
start. While Core is unreachable runs continue; events wait in the spool. A run whose terminal event cannot be
delivered keeps its spool until it is (the heartbeat keeps listing the run, as with a delayed NATS publish today).

### D6. Wake-ups
An in-process notifier with keys `(company, node)` and `company`: commits that create a `QUEUED` run, a stop,
reset or cleanup, a hooks change or a sync job signal it after the commit. A waiting poll returns on a signal and
also re-checks Postgres every 2 s, shared per company (one offers query per company per tick for all its waiting
polls), so a missed signal or a second Core process costs at most 2 s. Feed and run event SSE keep their delivery
loop and may use the same notifier.

### D7. No outbox
Commands are rows in `runs`; stops and node actions are run and task state; feed rows are `feed_events`; no
external consumer remains. The `outbox` table and its relay, `RedeliverQueued` (republishing), the Core consumer of
`FACTORY_EVENTS` and the publish of feed rows go. Transactions keep their atomic writes: dispatch persists the task
`IN_PROGRESS`, the run and its trigger rows in one transaction, as before. The reconciler keeps the queue TTL
(`unclaimed`, no attempt) and lease expiry. Mutation replay by request key is unchanged.

### D8. Removed (no migration, not released)
`internal/bus`, NATS in `compose.yaml` and `.env.example` (`NATS_URL`, `NATS_PASSWORD`), node.yaml `nats` (url,
creds, nkey, TLS, WebSocket), NATS self-checks and doctor checks, `protocol.CommandSubject`, `EventSubject`,
`FeedSubject`, `NodeControlSubject`, `SubjectCompany`, the `outbox` table, `scripts/nats-secure-test.sh`,
JetStream tests and the rule to run NATS tests with `-p 1` on a throwaway broker.

### D9. Load
One Core: one waiting request per node; Go keeps thousands of idle requests cheaply. Postgres: one offers query per
company per 2 s while its nodes wait, plus one per poll answer; indexed on `(company_id, status, created_at)` for
`QUEUED`.

## Risks / Trade-offs

- Core down means no new work, as today (claims are already HTTP); runs keep going and events wait in the spool.
- Offers to several nodes cause claim races; they are cheap 409s, and the oldest-first order keeps fairness.
- A long poll through a proxy with a shorter read timeout than 25 s is cut early; the node polls again and
  `wait_seconds` can be lowered in node.yaml.
- Losing JetStream's buffer for node events is replaced by the node spool; a node whose disk is lost before
  delivery loses those events, and its runs end as `lost` by the existing rules.
