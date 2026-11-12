# Billing

The SaaS charges the **account** (saas-billing): an account owns its companies, its owners see Billing in the
company menu (`/c/<company>/billing`), and people invited from other accounts never pay for the company they join.
App Factory pays for no model tokens (agents run on the customer's nodes and subscriptions), so the plans limit
what grows with the hosted coordination: own nodes and people.

## Plans

| Plan | Price | Own nodes | Inviting people | Cloud credits |
|---|---|---|---|---|
| Free | $0 | 1 | no (the owner alone) | none |
| Pro | $29 / month | 5 | yes | 2,000 a month (for cloud nodes, `hosted-connectors`; not spent anywhere yet) |

Prices are the owner's of 2026-11-06 and are revisited before launch; yearly prices come later; On-prem is a
postponed idea. Agents, companies, projects and history are not limited. The plans are `storage.Plans`
(`internal/storage/billing.go`); the landing's pricing section (`web/src/app/page.tsx`) repeats them.

A new account gets **14 days of Pro** at sign-up without a card, then Free unless it pays. Paying during the trial
starts the paid subscription at once (the trial is Core's, not Paddle's).

## What the limits do

Core enforces the plan, never the web or an agent prompt (`Store.PlanLimits`):

- **Connecting a node** (sign-in connection or enrollment code approval) is refused when the account already has
  as many own nodes as the plan allows, counted over every company of the account: 403 with reason `plan_limit`
  ("your Free plan connects 1 own node and you have 1: Pro connects 5 own nodes, see Billing"). Key rotation of
  a connected node is not a new node.
- **Inviting** (creating a link, and accepting an open link after a downgrade) is refused on a plan without
  invitations: 403, reason `plan_limit`.
- **After a downgrade nothing is lost.** Members stay; nodes over the plan stay connected but take no work: they
  get no offers, their claims are refused (`plan_limit`, retry) and a queued run says so (queued reason
  `plan_limit`). The nodes that take work are the most recently *picked* ones (Billing → Make it work,
  `POST /billing/nodes/{id}/pick`), then the oldest approved.
- An account **without a subscription row has no limits**: accounts created before billing was switched on, the
  `default` account, every self-hosted or dev Core. The operator sets a plan when wanted (below).

The web shows a refusal with a link to Billing; Settings → Members says that inviting starts with Pro; owners on a
trial see a banner with the days left.

## Configuration

| Variable | Meaning |
|---|---|
| `FACTORY_BILLING` | `off` (default): no limits, no trials. `on`: limits and trials, plans set by the operator. `paddle`: limits, trials and payments through Paddle. |
| `FACTORY_PADDLE_API_KEY` | Paddle API key (server-side; Railway secret). |
| `FACTORY_PADDLE_WEBHOOK_SECRET` | Secret key of the Paddle notification destination. |
| `FACTORY_PADDLE_PRICE_PRO` | Paddle price id (`pri_…`) of Pro monthly. |
| `FACTORY_PADDLE_ENV` | `sandbox` (default) or `production`; `FACTORY_PADDLE_API_URL` overrides the API address. |

Core refuses to start with `FACTORY_BILLING=paddle` and a missing key, secret or price.

Turning billing on for the hosted Core: existing accounts (including the owner's) have no subscription row, so
they keep working without limits; only accounts that sign up afterwards start on the trial. Give an existing
account a plan with `factory companies plan <company> free|pro|unlimited` (operator token;
`PUT /companies/{id}/plan`), which sets the plan of the company's account. `unlimited` is a complimentary plan.

## Paddle

Paddle Billing is the merchant of record: it is the seller, handles VAT and sales tax, invoices and refunds.
Core never sees card data.

- **Upgrade** (`POST /billing/checkout`): Core creates a Paddle transaction for the Pro price with
  `custom_data.account_id` and answers its `checkout.url`. Paddle builds that URL from the **default payment
  link** (Paddle → Checkout → Checkout settings): it must be set, to a page with Paddle.js or to a Paddle hosted
  checkout.
- **Manage** (`POST /billing/portal`): a customer portal session for the account's Paddle customer and
  subscription (card, invoices, cancel).
- **Webhooks**: a notification destination at `https://<core domain>/hooks/paddle` for the `subscription.*`
  events (created, updated, activated, past_due, paused, resumed, canceled). Core checks `Paddle-Signature`
  (`ts=…;h1=…`, HMAC-SHA256 of `ts:body` with the destination's secret, 5 minutes of tolerance, any `h1` during a
  secret rotation) before reading the body, finds the account by `custom_data.account_id` (copied from the
  transaction to the subscription), else by the subscription or customer id, and maps the price to the plan. Every
  event is stored once (`billing_events`): a redelivery is answered `duplicate`; an event older than the last
  applied one is `stale`; a cancellation of an old subscription does not end a newer live one. A storage failure
  answers 500 so Paddle retries. An event for an unknown account is recorded and logged as an error.
- Statuses: `active` and `past_due` (Paddle retries the payment) keep the paid plan; `paused` and `canceled` are
  Free. A cancellation scheduled in the portal shows "Pro until <date>"; Paddle sends `canceled` when it happens.

Not yet verified against a real Paddle account: the sandbox setup (product, price, default payment link,
notification destination) is the owner's to do; the code is tested against a fake Paddle API and fake signed
webhooks (`internal/billing`, `internal/core/billing_test.go`).

## Storage

`account_subscriptions` (one row per account: plan, status, trial end, period end, scheduled cancellation,
provider, customer and subscription ids, the last applied event time), `billing_events` (provider event ids,
outcome), `node_keys.picked_at` (migration `0035_billing.sql`). Usage (own nodes, people) is counted live.
