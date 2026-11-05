## 0. Decisions

- [ ] 0.1 Owner decides the metric, plans and prices, the provider, the trial and yearly prices (proposal).

## 1. After the decisions

- [ ] 1.1 Storage: plans, account subscription (plan, status, period end, provider ids), usage counts; tests.
- [ ] 1.2 Limits in Core: agent slots at claim time (a wait reason like a quota pause), companies, connectors,
  invitations and people on create; history retention by plan; tests.
- [ ] 1.3 Provider: hosted checkout and customer portal links; signed webhooks update the plan (idempotent);
  sandbox account; tests with recorded webhook payloads.
- [ ] 1.4 Web: Billing page (plan, usage against limits, upgrade, manage), limit messages linking to it; trial
  banner; browser check desktop and phone.
- [ ] 1.5 Docs: `docs/billing.md`; Terms and pricing page on the landing.
