# Calorie calculator: real autonomous-team E2E

Started 2026-10-13. Status: IN PROGRESS, not yet a passed E2E or a production release.

## Project and team

Project ID: `calorie-calculator`. Workspace: `projects/calorie-calculator` (absolute host path in Core).
Owner conversation: `calorie-owner-dialog`.
Kickoff message: `7859f98894a76f96e751c38ba9250d12`.
Initial manager run: `970c85eae67c54c8872faafdaff01b50`.

| Agent | Role |
| --- | --- |
| calorie-manager | Autonomous product/design/technical/quality decisions, planning and review |
| calorie-designer | Visual language, screen flows, states and implementable design handoff |
| calorie-ios | SwiftUI implementation, real builds/tests and release preparation |
| calorie-qa | Independent checks, defect reports and fix verification |

Dedicated YAMLs and prompts are under configs/agents and prompts. calorie-project policy grants
explicit category authority only to calorie-manager. Tools deny owner questions/approval requests
and owner-review proposals for this team; manager may answer the owner and send status notifications.
Specialists can contact their manager. Existing agents/policies retain their prior scope. These
are project-specific agent names in the global catalog, not a new enforced project-team ACL feature.

The owner requests a clean, minimalist, stylish native iOS app ready for production release.
Dedicated accessibility work/audit/testing is explicitly deferred by owner instruction. Full brief:
[PROJECT_BRIEF.md](../projects/calorie-calculator/PROJECT_BRIEF.md).

## Observe and communicate

From the factory directory:

```sh
set -a
source .env
set +a
python3 scripts/calorie-e2e.py status
python3 scripts/calorie-e2e.py watch
python3 scripts/calorie-e2e.py send "Какой сейчас статус?"
```

watch refreshes every five seconds; Ctrl-C stops observation, not the worker. It displays task
states, active/failed/unknown runs, owner inbox count and latest manager answer. --json on status
returns full persisted task/run/dialog state for inspection. start is checkpointed and reuses the
same kickoff request; do not create duplicate project runs manually to make a stalled task continue.

Equivalent existing CLI:

```sh
./bin/factory tasks list calorie-calculator
./bin/factory projects feed calorie-calculator
./bin/factory reports list calorie-calculator
./bin/factory decisions list calorie-calculator
./bin/factory conversations messages calorie-owner-dialog
./bin/factory inbox list calorie-calculator
./bin/factory conversations send calorie-owner-dialog - <<'JSON'
{"body":"Какой сейчас статус?"}
JSON
```

## Execution and acceptance

Core and authenticated NATS run in Docker. Native Worker logs/PID are under .cache/calorie-e2e;
Worker remains running for this project. Runs in the shared project workspace go one at a time (one writer slot);
worktree tasks hold their own slots. Agent caps (max_concurrent_runs) apply.
Each specialist submits and notifies the manager; manager reviews and creates/makes-ready the next
work. Communication runs drive orchestration without requiring the owner to manually advance stages.
A real unresolved developer question may exercise explicit answer/resume; no fabricated questions
or defects are required. App code must be built and independently checked on the installed toolchain.

The release gate includes real simulator checks, meaningful calculation/input/persistence tests,
Release builds, polished implemented UI and assets, privacy/data deletion and release materials.
It explicitly excludes dedicated accessibility audit/testing for this test. Manager must produce
an honest docs/RELEASE_READINESS.md assessment with concrete evidence and outstanding external
publication/signing/account/URL/device prerequisites. Actual App Store upload/publication or paid
external operations are not authorized by this local development assignment. No claim of production
publication or approval is made by the E2E setup.

Apple references supplied to the manager: [App Review Guidelines](https://developer.apple.com/app-store/review/guidelines/),
[Manage app privacy](https://developer.apple.com/help/app-store-connect/manage-app-information/manage-app-privacy),
[Submitting](https://developer.apple.com/app-store/submitting/).

## Setup checks

YAML catalog validation passed (7 agents total), manager product-decision delegation resolves to
allow, owner.ask resolves to deny. `make check` passed (race tests and vet). Xcode 27.0, XcodeGen,
Swift, OpenSpec and iOS simulators are installed. Core data was backed up before deployment to
.cache/calorie-team-backup/data. .env secrets were preserved. App workspaces are excluded from
the Docker build context; Core reads them through the configured workspace bind mount.

## Verified initial handoff

Manager kickoff completed and explicitly answered the owner. Four approved decisions were recorded
by calorie-manager under policy authority (product/technical/design/quality). The manager authored
and strictly validated OpenSpec change deliver-portion, initialized the app Git repository, and
created/made-ready designer task `0bffbc9de4dcbe41fcc5dc037aa9437f`. Designer run
`50b54b08ac3e9376a3a0a238aa0d3d5d` is RUNNING at setup handoff. No owner questions are pending.
Evidence: .cache/calorie-e2e/kickoff-verification.json. This verifies startup and the first handoff,
not the eventual complete app/release gate. Native Worker remains running for subsequent stages.
