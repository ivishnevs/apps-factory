## 0. Decisions

- [x] 0.1 Metric and plans (2026-11-05): own connectors Free 1 / Pro 5 / Team unlimited, invitations from Pro,
  cloud credits in Pro and Team.
- [x] 0.2 Paddle, 14-day Pro trial without a card, yearly later; plans Free / Pro / On-prem (owner 2026-11-06); prices open.
- [x] 0.3 Prices for now: Free $0, Pro $29 with 2,000 credits; On-prem postponed as an idea (owner 2026-11-06).

## 1. After the decisions

- [x] 1.1 Storage: plans, account subscription (plan, status, period end, provider ids), usage counts; tests.
  Done: migration `0035_billing.sql` (`account_subscriptions`, `billing_events`, `node_keys.picked_at`); plans in Go
  (`storage.Plans`: Free, Pro, plus `unlimited` for no-plan/complimentary accounts); usage (own nodes, people)
  counted live; `internal/storage/billing.go`, `billing_test.go`.
- [x] 1.2 Limits in Core (can start before the provider: plan set by the operator): own connectors on connect and
  on claim (over the limit after a downgrade takes no work), invitations only from Pro; tests.
  Done: node approval (sign-in connection and enrollment code) and invitations (create and accept) refused with
  403 `plan_limit`; nodes over the plan get no offers, claims refused (`plan_limit`, retry), queued reason
  `plan_limit`; the owner picks which nodes work (`POST /billing/nodes/{id}/pick`); operator
  `factory companies plan ID free|pro|unlimited`. `FACTORY_BILLING` unset/off: no limits.
- [x] 1.3 Provider: hosted checkout and customer portal links; signed webhooks update the plan (idempotent);
  sandbox account; tests with recorded webhook payloads.
  Done: Paddle Billing (`internal/billing`): checkout = transaction `checkout.url`, portal = customer portal
  session, `/hooks/paddle` verifies `Paddle-Signature`, events applied once and in order; tests with a fake Paddle
  API and fake signed webhooks shaped like Paddle's documented payloads. **Blocker:** the Paddle sandbox account
  (product, Pro price, default payment link, notification destination, keys) is the owner's to set up and check.
- [x] 1.4 Web: Billing page (plan, usage against limits, upgrade, manage), limit messages linking to it; trial
  banner; browser check desktop and phone.
  Done: `/c/<company>/billing` (owners; in the company menu when billing is on), plan-limit refusals with a link
  or a toast button to Billing, invitations disabled on Free, trial banner. Checked in a browser on desktop (trial,
  Free over the limit, picking a node); the window could not be narrowed to a phone width, the page uses the
  responsive layout of the other pages (stacked cards below `sm`).
- [x] 1.5 Docs: `docs/billing.md`; Terms and pricing page on the landing.
  Done: `docs/billing.md`, a Pricing section on the landing (`/`), a pointer in `deploy-railway.md`.
  **Blocker:** Terms (legal text) need the owner.
- [ ] 1.6 (postponed idea) On-prem: Core installable by customers (package, migrations, backups, upgrades, licence).
  Skipped: postponed by the owner (2026-11-06).
