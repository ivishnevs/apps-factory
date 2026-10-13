# Reports and Decision Registry verification

Verified 2026-10-12, macOS arm64 native Codex Worker against Docker Core and authenticated NATS
JetStream. Before migration 7, the stopped Core data directory was copied to
.cache/pre-reports-decisions-backup/data. Core was rebuilt and migrated without resetting volumes.

`make check` passed (all packages with race detection, then go vet); `make build` passed.
A dedicated temporary JetStream broker on 127.0.0.1:14222 passed
`NATS_TEST_URL=nats://127.0.0.1:14222 go test -race ./internal/worker -run JetStream -count=1`.
That broker was stopped and removed; production streams were not used by integration tests.

The opt-in `python3 scripts/journal-smoke.py --restart-core` completed project
journal-smoke-ff5ac783a9 with three real Codex runs:

| Agent | Run | Result |
| --- | --- | --- |
| demo | c6872304f67ab3d2b0c67e1b499023c6 | Created proposal and significant report; JOURNAL_PRODUCER_OK |
| manager | f5fa3394b9cf24163551c72246dac6b0 | Read context, decisions, full producer report and feed; JOURNAL_CONSUMER_OK |
| demo | 81a8e7d62f7ee36dc00a52cc5ba65112 | Read replacement and superseded history; JOURNAL_REVISED_OK |

All tasks were reviewed to DONE. All journal MCP audit calls returned 200. Six reports persisted:
three source=agent stage reports and three source=core observed COMPLETED outcomes.
Accepting the producer task and reading its notifications did not approve its proposal. The
pending proposal survived Core restart. Owner separately approved decision
823d6777d3a68fbd5801d2a0f89d9e3b (SQLite). Replacement without the current ID/version failed with
409; explicit replacement approved 307592098a1fc6671506bc1de1412a58 (PostgreSQL), superseding the old
record. The manager run retained SQLite in its immutable snapshot; the next demo run captured
PostgreSQL. Reading the reports/decisions/feed via native CLI also passed, including pagination.

Unit tests additionally verify agent decision recording needs an exact action grant, changed
arguments remain blocked, run tokens cannot call owner routes, identity forgery is rejected,
foreign project reads and owner-communication bypasses are denied, concurrent replacements have
one winner, UTF-8 context is bounded, report retries are immutable, artifact references must match
registered content, report/proposal notifications do not block, and UNKNOWN reports deduplicate
and close their notice after explicit reconciliation.

Raw local metadata: .cache/reports-decisions-verification.json and
.cache/journal-final-verification.json (no credentials). Final inspection found no
QUEUED/RUNNING/CANCELLING/UNKNOWN runs and an empty test-project inbox. The original Foundation run
605503cdb856206b24667f70583db458 remains COMPLETED with FOUNDATION_OK. Native Worker was stopped
cleanly; Core and NATS remain running.
