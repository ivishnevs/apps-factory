## 0. Decisions and spike

- [ ] 0.1 Owner decides naming, margin, keys (ours or bring-your-own), packs and salary fund, sizes and idle time.
- [ ] 0.2 Spike: Fly.io Sprites API (create, suspend and resume times, disk persistence, pricing per second, limits,
  networking to Core); a `factory-node` image runs in a Sprite and claims a task. Result written into `design.md`.

## 1. Cloud connectors

- [ ] 1.1 `internal/cloud`: Sprites client; cloud connector lifecycle (create with pre-approved keys, suspend when
  idle, resume for claimable work, destroy); tests against a fake API.
- [ ] 1.2 Model access: our keys (per-company metering) or the company's API key secret; runtimes use API keys on
  cloud connectors; tests.
- [ ] 1.3 Web: Connectors → "Run in App Factory cloud" with sizes; the setup wizard offers it next to "Your
  machine"; browser check.

## 2. Payroll

- [ ] 2.1 Storage: wallet and entries (top-up, run charge, refund), salary caps per agent and team, company budget;
  tests.
- [ ] 2.2 Metering: compute seconds and tokens per run priced with the margin, booked to the agent; daily
  reconciliation report; tests.
- [ ] 2.3 Dispatcher: empty wallet and reached caps as wait reasons with escalations to raise them; tests.
- [ ] 2.4 Web: Costs → Payroll (per agent and team), wallet top-up and auto top-up, caps; low-balance warnings;
  browser check.
- [ ] 2.5 Top-ups through the `saas-billing` provider; plans' monthly salary fund.
