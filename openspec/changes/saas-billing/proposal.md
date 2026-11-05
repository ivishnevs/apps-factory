Status: proposal — needs the owner's decisions (section "Decisions needed"). Nothing is implemented.

## Why

The SaaS has no way to charge. What App Factory sells is different from a usual AI product: the agents run on the
customer's machines (connectors) and on the customer's own Claude and Codex subscriptions, so App Factory pays for
no model tokens. It hosts the coordination: Core, the web, tasks, chats, the catalog, secrets, skills and
notifications. The price should follow what grows with the value and with our cost: how much work a company runs
through Core, not tokens.

## What Changes (proposed)

- **Who pays: the account.** An account is the billing owner of its companies (team-invitations); people invited
  from other accounts never pay for the company they join. The account's owner sees Billing in the account menu.
- **Plans with limits, checked by Core.** A plan sets: companies per account, connectors per company, agent slots
  in use at once (the concurrency that matters for us), people per company, and history kept (feed, logs, run
  records). Proposed starting point (numbers are placeholders for the owner):

  | Plan | Price | Companies | Connectors | Agent slots at once | People | History |
  |---|---|---|---|---|---|---|
  | Free | 0 | 1 | 1 | 2 | 1 | 7 days |
  | Pro | $29 / month | 3 | 3 | 8 | 5 | 90 days |
  | Team | $99 / month | 10 | 10 | 30 | 25 | 1 year |
  | Enterprise | by agreement | — | — | — | — | — |

  The cost ledger already records every run's tokens and dollars per agent (the customer's subscription usage); it
  stays an analytics view for the customer, not a billing meter.
- **Over a limit nothing is lost.** New work waits with a reason ("your plan runs 2 agents at once") exactly like a
  quota pause; creating a company, connector or invitation over the limit is refused with a link to Billing.
  Downgrading keeps everything and only stops what is over the limit from starting.
- **Payment provider: a merchant of record** (Paddle or Lemon Squeezy) rather than plain Stripe: they act as the
  seller, so VAT and sales tax across countries, invoices and refunds are theirs, which matters for a small team
  selling worldwide. Stripe stays the option if the owner already has a company and tax setup for it.
- **Flow.** Billing page: current plan, usage against limits, Upgrade → the provider's hosted checkout; Manage →
  the provider's customer portal (card, invoices, cancel). The provider's signed webhooks update the account's plan
  in Core; Core never sees card data.
- **Trial.** 14 days of Pro for a new account, then Free unless they pay; no card at sign-up.

## Decisions needed

1. The metric: agent slots at once as the main limit (proposed), or connectors, or active agents, or seats?
2. Plans and prices (the table above is a placeholder), and whether there is a Free plan.
3. The provider: Paddle / Lemon Squeezy (merchant of record) or Stripe (owner handles taxes)?
4. Trial: 14 days of Pro without a card (proposed), or none?
5. Yearly prices with a discount now or later?

## Impact

`internal/storage` (plans, account subscription, usage counts), `internal/core` (limit checks at claim time and on
creating companies, connectors, invitations; provider webhook route), the dispatcher (a plan limit as a wait
reason), web Billing page and the limit messages, `docs/billing.md`, a new `saas-billing` spec.
