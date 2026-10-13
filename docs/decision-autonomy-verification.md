# Decision autonomy verification — 2026-10-12

## Automated checks

`make check` passed: `go test -race ./...` and `go vet ./...`. `make build` passed.
Additional focused `go test -race ./internal/core -run TestDecision -count=1` passed after
extending owner-choice preservation checks. `openspec validate decision-autonomy --strict --json
--no-interactive` passed. The Python smoke script compiles.

Covered: explicit category delegation in captured/current config, absent/revoked/denied authority,
project identity, idempotent creation and changed-request conflicts, explicit policy replacement,
owner-approved choice protection, rejection retaining the previous approved choice, pending/rejected
topic takeover denial, owner communication denial and protected operation approval requirements.
Rejection replay produces one message; it cannot overlap a current writer; after SQLite reopen,
message scheduling produces one separate revision run with the rejection reason.

## Real Docker Core + authenticated JetStream + native Codex

Run `scripts/autonomy-smoke.py` with `.env` exported and native Worker running. It writes an immediate
checkpoint and supports `--resume CHECKPOINT`. Evidence remains in the project state, checkpoint and
`.cache/decision-autonomy-verification/live.json`. Core was backed up before deployment to
`.cache/decision-autonomy-backup/data`.

Project: `autonomy-smoke-96d6c62565`.

| Run | Result |
| --- | --- |
| `9e6a315e2335c6b18955690237793e19` | manager-autonomous recorded technical SQLite choice; exact retry returned the same ID; product decision and owner question denied with HTTP 403 |
| `b9825328ad0e4039f56dd71a43f52658` | manager proposed product pricing choice without approving it |
| `2972826e35ebdd237e8a7afd9d1650f8` | Core automatically delivered rejection; manager blocked affected work, proposed Free offline app and recorded plan_deviation |

All three runs COMPLETED; both producer tasks DONE after explicit test-owner review. Autonomous
decision `29ae50a52b01ec6db26a31032338cb9c` has authority policy, category technical and actual
approver manager-autonomous, rules and both configuration hashes. It created no owner notification.

Owner rejected `faad77eb7a0f6bb6a6eb55dd71b009fc`; replay returned the same record and reaction
message `7e6e9a56e8cfc70f24b0da7541b337b3`. Linked task
`61ef77dd636b5246ba70a551de17439d` became BLOCKED. The alternative remains a proposal, not an
approved decision. The original technical decision is still the only approved choice.

All revision tool calls returned HTTP 200. The only deliberate failures in the scenario were the
two 403 permission checks. Core restart preserved registry, task block, report and completed
message delivery. No QUEUED/RUNNING/CANCELLING/UNKNOWN runs remain. Worker was stopped after
verification; Core and NATS remain running.

## Limits

The revision plan is agent behavior, verified here on a real model; durable dispatch and category
checks are deterministic. Owner veto is matched by normalized topic text; semantic renaming is
not detected. Factory permissions constrain gateway tools; trusted native Codex still has host
shell access. No automatic rollback, publication/purchase executor or blanket authority was added.
