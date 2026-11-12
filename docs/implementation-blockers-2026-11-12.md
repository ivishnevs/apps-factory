# Implementation run 2026-11-12: what is left and why

The owner asked to implement every OpenSpec change that was ready (no open questions, no blockers). This lists what
could not be finished and what it waits on. Each item is also noted in its change's `tasks.md` and proposal.

## Needs the owner's accounts, money or decisions

| Change | Task | Waits on |
|---|---|---|
| saas-billing | 1.3 live | A Paddle sandbox: product and Pro price, default payment link, notification destination `https://<core>/hooks/paddle`, one real payment and cancellation. Code is done and tested with a fake Paddle. |
| saas-billing / saas-release-readiness | 1.5 Terms, 4.1 | Lawyer review and owner approval of `docs/legal/*.DRAFT.md`; then publish and link them. |
| saas-release-readiness | 1.1 | Production Railway project, custom domains, OAuth apps (GitHub, Google). |
| saas-release-readiness | 1.3 | `FACTORY_SECRETS_KEY` backup outside Railway; the rotation procedure is not written yet. |
| saas-release-readiness | 2.1, 2.2 | Postgres backups and restore drill, blob volume backup, uptime checks and alerts on the production project. |
| saas-release-readiness | 5.1 | The owner's choice of mail provider. |
| connector-trust | 2.2 | Apple developer account for signing and notarization; Linux package signing key; SBOM in releases. |
| connector-trust | 2.3 | Publishing the node source (Apache-2.0). |
| connector-trust / saas-release-readiness | 3.2 | External penetration test. |
| connector-trust | 1.5 | Isolated mode: the owner decided "later". |
| approval-comments-handled | 2.1 (prompts) | The tvorog Launch Creative and Reviewer prompts live only in the prod catalog: remove their approval-comment lines after deploy. |

## Needs a deploy or real machines

| Change | Task | Waits on |
|---|---|---|
| node-packaging-updates | live check of 2.2, 3.1–3.4, 3.6 | Core deploy, then one manual reinstall of the Mac and hetzner-hel1 with a package from this change; watch one real update and rollback under launchd/systemd. Codex pins: only darwin-arm64 was downloaded and verified. |
| node-packaging-updates | 3.5 | Claude without the JS shim: owner decision 4, only after the update flow passes live smokes. |
| inbox-visual-previews | 4.1 live | A sandbox Core with the real Telegram bot to see the images in the live inbox, chat and Telegram. |
| saas-billing | 1.4 | Billing page not seen at phone width. |

## Needs material that does not exist in the repository

| Change | Task | Waits on |
|---|---|---|
| setup-assistant | 2.2 `factory import paperclip` | A real Paperclip export or recorded API responses; no format was invented. |

## Not started, and why (checked against each change on 2026-11-12)

| Change | Why it was not implemented |
|---|---|
| milestones | Four open questions wait for the owner (`proposal.md` "Open questions (with recommendations)"): active milestones per project, tasks outside the active one, source of truth, ETA. Answering "as recommended" makes it ready. |
| team-page-clarity | Task 1.1 is "wording and layout …; owner approves": a design must be approved before any code (Status: "not designed or implemented"). |
| park-watchdog | Status "idea (not scheduled; the owner wants to think about it)"; task 1.1 "Owner decides whether to adopt …". Recommendation: close it, its cases are covered by throughput-signals, decision-waits and inbox-badge-parity. |
| agent-context-window | "Deferred by the owner 2026-11-07" after the research; task 1.2 is also blocked on an upstream Codex issue (#16033). |
| hosted-connectors | Everything depends on spike 0.2 (Fly.io Sprites: a real account and paid machines, `factory-node` claiming a task in a Sprite); the decisions in 0.1 "may change with what it shows". Needs the owner's Fly.io account and budget. |

## Partly implemented: the exact open tasks

| Change | Open task | Reason |
|---|---|---|
| approval-comments-handled | 2.1 prompt cleanup | Prompts exist only in the prod catalog (no prod changes in this run). |
| inbox-visual-previews | 4.1 live check | Real bot and live inbox; covered by tests with a fake Bot API. |
| node-packaging-updates | 3.5 | Owner decision 4 (2026-11-12): only after the update flow passes live smokes. |
| saas-billing | 1.6 | Postponed idea (On-prem) by the owner; 1.3 live Paddle check and 1.4 phone-width check as above. |
| saas-release-readiness | 1.1, 1.3, 2.1, 2.2, 5.1 | Owner's production accounts and choices (tables above). |
| saas-release-readiness | 3.2 | External penetration test (connector-trust 3.2). |
| saas-release-readiness | 6.2 | `hosted-connectors` done, or the launch decision to go without cloud nodes (owner). |
| setup-assistant | 2.2 | No Paperclip export format available. |
| connector-trust | 1.5, 2.2, 2.3, 3.2 | Isolated mode "later" (owner); signing, source publishing and pentest need accounts and an external firm. |

## Recommended follow-ups (not blockers)

- Node.js for the Claude single executable is still 22.18.0; six 22.x security releases since (latest 22.23.5).
  Move in its own change with a node package rebuild and a self-update test (`docs/dependency-updates-2026-11.md`).
- 14 Low/informational security findings (O1–O14) in `docs/security-review-2026-11.md`, e.g. rate limits trust
  `X-Forwarded-For`, owner tokens never expire, 500 answers carry internal error text.
- `make check` runs `workers/claude` `npm test` without building `dist/`, so it reports 0 tests; build first.
- Timing-sensitive tests flaked under parallel load (`TestParallelScheduleClaimExpire`, `TestTurnCompletedBeforeExitIsTerminal`,
  `TestRunStoppedAndPrune`, `TestEventsSpool`); all passed when re-run.
- Presets no longer pin `max_concurrent_runs: 1` (throughput-signals); companies created from a preset keep their
  explicit 1 until it is removed from their profile.
