## 0. Decisions

- [x] 0.1 Metric and plans (2026-11-05): own connectors Free 1 / Pro 5 / Team unlimited, invitations from Pro,
  cloud credits in Pro and Team.
- [x] 0.2 Paddle, 14-day Pro trial without a card, yearly later; plans Free / Pro / On-prem (owner 2026-11-06); prices open.
- [x] 0.3 Prices for now: Free $0, Pro $29 with 2,000 credits; On-prem postponed as an idea (owner 2026-11-06).

## 1. After the decisions

- [ ] 1.1 Storage: plans, account subscription (plan, status, period end, provider ids), usage counts; tests.
- [ ] 1.2 Limits in Core (can start before the provider: plan set by the operator): own connectors on connect and
  on claim (over the limit after a downgrade takes no work), invitations only from Pro; tests.
- [ ] 1.3 Provider: hosted checkout and customer portal links; signed webhooks update the plan (idempotent);
  sandbox account; tests with recorded webhook payloads.
- [ ] 1.4 Web: Billing page (plan, usage against limits, upgrade, manage), limit messages linking to it; trial
  banner; browser check desktop and phone.
- [ ] 1.5 Docs: `docs/billing.md`; Terms and pricing page on the landing.
- [ ] 1.6 (postponed idea) On-prem: Core installable by customers (package, migrations, backups, upgrades, licence).
