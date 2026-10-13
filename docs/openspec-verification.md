# OpenSpec integration verification

Verified 2026-10-12 on macOS arm64, native Go/Codex Worker with Docker Core and authenticated NATS.
SQLite remains schema 7; no document storage table or new dependency was added. Before deployment,
the stopped Core data directory was copied to .cache/pre-openspec-integration-backup/data. Existing
.env credentials were preserved and an explicit host workspace root was added.

`make check` (go test -race ./... and go vet ./...) and `make build` passed. Focused additional
OpenSpec tests also passed with race detection, including Core's defensive runtime-audit filter
and broken-link handling in a known-failure retry. Official OpenSpec 1.14.0 validated the repository's
openspec-integration change with --strict --json --no-interactive.

A dedicated temporary broker on 127.0.0.1:14222 passed:

```sh
NATS_TEST_URL=nats://127.0.0.1:14222 go test -race ./internal/worker -run JetStream -count=1
```

This includes Foundation, cancellation, Task Engine restart and the new test proving a missing
native OpenSpec reference emits known FAILED evidence before any runtime executable is started.
The temporary broker was stopped and removed. Production streams were not used by those tests.

## Real workflow

Project: openspec-smoke-e5a55672f6, workspace under .cache of this repository.

| Agent | Task | Run | Evidence |
| --- | --- | --- | --- |
| manager | a943bd766584ee3b63716db74935f299 | fb803b20e0b9d26f2b8652ff0d9f1dee | Official CLI created add-greeting; proposal/design/tasks/delta spec authored; linked executor created and readied |
| demo | 4c366a735ab67fb76c96e30bbd80e79b | 7c649f6fd7dc3f423621c12310bac39f | Required files read through MCP; greeting.txt bytes checked; structured submission |
| manager | cb36ce298ce827c564509ad93dabb53a | 35918ad78f697f5b727d979da9c943f6 | Current proposal edit read; official strict validation and byte verification; executor explicitly accepted |

All three runs are COMPLETED and tasks DONE. greeting.txt is exactly b'HELLO_OPENSPEC\n'; SHA256
9118084793ddeac02d2cfca6a235d18f6c408f6ac99dad012db1c27147515d0c. OpenSpec checkboxes were already
complete while the executor was still IN_REVIEW; only factory_task_accept changed it to DONE.

Owner initialization and replay created the directories on the native host through the Docker
mount without overwriting files. Missing task links returned 400. Proposal content and digest
survived Core restart. A subsequent native edit changed SHA256 from
a52c4af0ccdc468c5f48b5f2848ada5ebdf2dd16e1eaa2f2a52f0d1430de61dc to
25346c4b31f36fb49c1b02a28bc1a45b8da4664f4a31a7979b8a854ae818488b; a read with the old digest
returned 409. Native CLI init/list/check/read also passed, including a bounded 32-byte page.

All OpenSpec MCP audit calls returned 200. Seven read results in runtime telemetry contained
metadata with document_body_omitted, preserving the real response only for the runtime itself.
One unrelated developer report attempt returned 400 for an invalid report kind; the agent
recovered by submitting a valid stage_completed report. No 5xx calls occurred. Six significant/
runtime reports persisted for the three runs.

The initial harness assertion read a host edit before Docker Desktop had propagated it. The
harness now waits for the expected digest and saves a checkpoint. Verification continued using
--resume openspec-smoke-e5a55672f6 after inspecting the saved tasks and matching the old digest to
actual MCP audit metadata; neither planning nor implementation was repeated. The inspection-only
audit was also corrected to require successful OpenSpec calls and retain recovered 4xx calls,
instead of assuming an autonomous agent never makes an invalid unrelated call.

Raw local evidence: .cache/openspec-verification.json and .cache/openspec-final-verification.json.
Final inspection found no QUEUED/RUNNING/CANCELLING/UNKNOWN runs and an empty project inbox.
Foundation run 605503cdb856206b24667f70583db458 still contains FOUNDATION_OK. The prior approved
Decision Registry replacement remains approved. Native Worker was stopped cleanly; Docker Core
and NATS remain running.
