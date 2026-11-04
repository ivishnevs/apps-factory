Status: planned (owner decision 2026-11-04: remove NATS, nodes talk to Core over signed HTTPS only; this takes the
postponed decision of the known issue in `docs/backlog.md`). Not started. Must land before the first node of an
external customer connects.

## Why

- **NATS does not isolate companies** (`docs/backlog.md`, known issue): a modified node with a NATS login for one
  company damaged other companies' streams and consumers in a security review on NATS 2.11.4, and revoking a node
  (`node-identity`) does not cut its NATS login. Nodes are the customers' own machines (`company-teams`,
  `node-identity`), so this blocks the SaaS.
- **NATS carries little that HTTP and Postgres do not already carry.** It has three uses:
  - run commands (`FACTORY_COMMANDS`): the run is already `QUEUED` in `runs`, the node takes its spec from the
    signed HTTP claim answer and treats the command as a pointer (`docs/runtime-protocol-v2.md`), and
    `RedeliverQueued` republishes lost commands from the database;
  - urgent stops on the per-node control subject: the heartbeat answer already stops a run when the message is
    missed (`authority-input`, `node-agent`);
  - run events from nodes to Core: Core applies them to Postgres only with the run's lease.
  Live UI updates are server-sent events from Core and do not use NATS.
- **Two transports with two authentication systems**: signed HTTP (`node-identity`) and NATS logins with their own
  permissions, revocation gap, TLS/WebSocket setup, `scripts/nats-secure-test.sh` and broker-bound tests (`-p 1`).
  Customer networks need outbound access to one more endpoint.

## What Changes

- **Long poll for node work:** `POST /nodes/{node_id}/poll` (signed) waits up to 25 s and answers at once when
  there is something for the node: offers of queued runs it can take, urgent stops, resets, cleanups, hooks
  changes and skill source sync jobs. The node claims an offered run with the existing signed claim.
- **Run events over HTTP:** `POST /runs/{run}/events` (signed, the run's node, with the lease) in ordered,
  idempotent batches. The node spools events on disk while Core is unreachable and sends them in order later.
- **Wake-ups inside Core:** commits that create work or stops wake the node's waiting poll; a short periodic check
  inside the poll covers missed wake-ups and a second Core process.
- **The outbox and NATS are removed:** commands are the `runs` table, stops are run state, feed rows are
  `feed_events`. `internal/bus`, JetStream streams and consumers, the control subject, the outbox relay, NATS in
  `compose.yaml`, node.yaml `nats`, NATS doctor checks, `scripts/nats-secure-test.sh` and the `-p 1` rule for NATS
  tests go. No migration (not released).
- The known issue in `docs/backlog.md` is closed.

## Out of scope

Postgres `LISTEN/NOTIFY` for several Core processes (the periodic check covers it), WebSocket transport, live
streaming of runtime output beyond today's run events, a message broker inside Core.

## Impact

`internal/core` (poll, offers, events route, notifier, gateway rules), `internal/storage` (offers query, event
apply by HTTP, outbox and redelivery removed, reconciler), `internal/node/runner` and `internal/worker` (poll loop,
claim from offers, event spool, NATS removed), `internal/protocol` (poll and events messages; subjects removed),
`internal/bus` (removed), `cmd/factory`, `cmd/factory-node`, `compose.yaml`, `.env.example`, `scripts/`, tests that
use JetStream, docs `architecture.md`, `development.md`, `runtime-protocol-v2.md`, `backlog.md`.
