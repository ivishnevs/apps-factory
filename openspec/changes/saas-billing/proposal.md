Status: plans decided 2026-11-05 (connectors as the metric, invitations from Pro, cloud credits in Pro and Team);
prices, provider and trial still need the owner. Nothing is implemented.

## Why

The SaaS has no way to charge. What App Factory sells is different from a usual AI product: the agents run on the
customer's machines (connectors) and on the customer's own Claude and Codex subscriptions, so App Factory pays for
no model tokens. It hosts the coordination: Core, the web, tasks, chats, the catalog, secrets, skills and
notifications. The price should follow what grows with the value and with our cost: how much work a company runs
through Core, not tokens.

## What Changes (proposed)

- **Who pays: the account.** An account is the billing owner of its companies (team-invitations); people invited
  from other accounts never pay for the company they join. The account's owner sees Billing in the account menu.
- **Plans (decided 2026-11-05): the limit is connectors of one's own.**

  | Plan | Price (placeholder) | Own connectors | Inviting people | Cloud credits included |
  |---|---|---|---|---|
  | Free | 0 | 1 | no (the owner alone) | none, cloud not available |
  | Pro | $29 / month | 5 | yes | yes, e.g. 2,000 credits a month |
  | Team | $99 / month | unlimited | yes | yes, more, e.g. 8,000 a month |

  Cloud connectors (`hosted-connectors`) do not count against the own-connector limit; they are paid by credits.
  Agent slots, companies and history are not limited by the plan for now. The cost ledger stays an analytics view
  of the customer's own subscription usage, never a bill.
- **Included credits are a selling point at retail value.** "Pro $29 / month: 5 connectors, your team, and $20 of
  cloud work included": the credits are priced at retail (cost plus margin), so $20 of included cloud work costs
  us about $15 (see `hosted-connectors`, "Pricing of credits").
- **Over a limit nothing is lost.** New work waits with a reason ("your plan runs 2 agents at once") exactly like a
  quota pause; connecting a connector over the limit or inviting on Free is refused with a link to Billing ("Pro lets you invite
your team").
  Downgrading keeps everything and only stops what is over the limit from starting.
- **Payment provider: a merchant of record** (Paddle or Lemon Squeezy) rather than plain Stripe: they act as the
  seller, so VAT and sales tax across countries, invoices and refunds are theirs, which matters for a small team
  selling worldwide. Stripe stays the option if the owner already has a company and tax setup for it.
- **Flow.** Billing page: current plan, usage against limits, Upgrade → the provider's hosted checkout; Manage →
  the provider's customer portal (card, invoices, cancel). The provider's signed webhooks update the account's plan
  in Core; Core never sees card data.
- **Trial.** 14 days of Pro for a new account, then Free unless they pay; no card at sign-up (proposed).
- **Downgrade to Free:** members stay but nobody new can be invited; connectors over the limit stay connected but
  only the first one takes work (the owner picks which); unused credits stay until they expire.

- **Usage on cloud connectors** is not part of the plan's price: it is paid in credits (`hosted-connectors`); Pro
  and Team include a monthly amount, extra packs are bought through the same provider.

## Decided (2026-11-05)

- Metric: own connectors (Free 1, Pro 5, Team unlimited); inviting from Pro; cloud credits in Pro and Team.

## Decisions needed

1. Prices ($29 / $99 are placeholders) and how many credits each plan includes.
2. The provider: Paddle / Lemon Squeezy (merchant of record) or Stripe (owner handles taxes)?
3. Trial: 14 days of Pro without a card (proposed), or none? Yearly prices now or later?

## Impact

`internal/storage` (plans, account subscription, usage counts), `internal/core` (limit checks on connecting connectors and
on invitations; provider webhook route), the dispatcher (connectors over the limit take no work), web Billing page and the limit messages, `docs/billing.md`, a new `saas-billing` spec.
