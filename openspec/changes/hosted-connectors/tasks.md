## 0. Decisions and spike

- [ ] 0.1 Owner decides naming, markup, keys (ours or bring-your-own), packs and expiry, sizes and idle
  time, default placement.
- [ ] 0.2 Spike: Fly.io Sprites API (create, suspend and resume times, disk persistence, pricing per second, limits,
  networking to Core); a `factory-node` image runs in a Sprite and claims a task. Result written into `design.md`.

## 1. Cloud connectors

- [ ] 1.1 `internal/cloud`: Sprites client; cloud connector lifecycle (create with pre-approved keys, suspend when
  idle, resume for claimable work, destroy); tests against a fake API.
- [ ] 1.2 Model access: our keys (per-company metering) or the company's API key secret; runtimes use API keys on
  cloud connectors; tests.
- [ ] 1.3 Web: Connectors → "Run in App Factory cloud" with sizes; the setup wizard offers it next to "Your
  machine"; browser check.

## 2. Credits and cloud budgets

- [ ] 2.1 Storage: credit balance and entries (included monthly, bought, run charge, refund, expiry), cloud budgets
  per team and company, "where a team works" setting; tests.
- [ ] 2.2 Metering: compute seconds and tokens per cloud run priced with the markup, booked to the agent; daily
  reconciliation with Fly and model invoices; tests.
- [ ] 2.3 Dispatcher: placement by "where a team works" (own only, overflow, cloud only); no credits or a used-up
  cloud budget as wait reasons that never touch own connectors; tests.
- [ ] 2.4 Web: Costs split into Your machines / Cloud; credit balance, packs and auto top-up; cloud budgets and
  "where a team works" on team pages; low-balance warnings; browser check.
- [ ] 2.5 Packs through the `saas-billing` provider; credits included in Pro (Team dropped 2026-11-06).
