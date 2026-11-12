Status: naming decided 2026-11-06 (virtual nodes; connectors renamed nodes); remaining decisions and a technical
spike on Fly.io Sprites still needed. 2026-11-12: the owner will provide a Fly.io account for the spike (0.2).
Updated 2026-11-05: own connectors and cloud work kept apart (budgets, Costs), naming options, credit pricing.
2026-11-12: architecture decided (`design.md`): an isolated virtual nodes manager, Core untouched (a virtual node
is an ordinary node). Nothing is implemented. Raised by the owner on 2026-11-05.

## Why

Today every company brings its own machine (a connector) and its own Claude and Codex subscriptions. That is the
cheapest way to run, but it is the hardest step of onboarding: a person who only wants to try App Factory has to
install something on a Mac or a server first. A **cloud connector** — a machine App Factory runs for the company —
removes that step: in the setup wizard and on Connectors the person picks "Run in App Factory cloud" instead of
"Your machine" and work starts.

On our machines the customer's personal subscriptions cannot be used (they are personal and tied to their
devices), so agents there run on API keys and every run costs real money in compute and tokens. A subscription
alone does not cover that: the company pays for usage, with a small margin over our cost.

## What Changes (proposed)

### Cloud connectors on Fly.io Sprites

- A Sprite (Fly.io's persistent, quickly resumed sandbox VM) per cloud connector, created by Core through the
  Fly API: the same `factory-node` with its runtimes (Codex, Claude), started with a connection already approved
  (no browser sign-in: Core creates its keys), so it is an ordinary connector to the rest of the system.
- Sizes as presets (e.g. Small: 2 agent slots, Medium: 4, Large: 8); the company chooses one when adding it.
- **Sleep when idle:** the Sprite is suspended when no run is going for N minutes and resumed when a task is
  claimable for it, so compute is paid only while agents work. Worktrees and caches stay on its disk.
- Isolation: one company per Sprite, no shared disk; the company's secrets reach it sealed as for any node.
- Model access, two options per company:
  - **Our keys (default):** App Factory's Anthropic and OpenAI API keys; tokens are charged to the company.
  - **Bring your own API key:** the company's Anthropic / OpenAI API key as a company secret; then only compute is
    charged.

### Paying for usage

**Two worlds that must never mix** (the owner's concern, 2026-11-05). Work on the customer's own connectors is paid
by their Claude / Codex subscriptions and is never charged or limited by credits; work on cloud connectors is paid
by credits. So:

- **Every budget and limit for credits applies to cloud work only** and says so in its name ("cloud budget").
  Subscription quotas (the existing quota pauses of own connectors) stay as they are and say "your Claude
  subscription".
- **Where a team works** is one setting per team, in plain words:
  - *Your machines only* (default for a company with a connector): credits are never used.
  - *Your machines first, cloud when they are busy or offline:* overflow; when the cloud budget is used up, work
    simply waits for your machines — no escalation, the reason says "waiting for your machine (cloud budget used up
    for today)".
  - *Cloud only* (default for a company without a connector).
- **Costs shows two separate sections:** "Your machines — paid by your subscriptions, not charged by us" (the
  existing token estimates) and "Cloud — charged in credits". Never one total that adds them up.
- A run shows where it ran (machine name or "App Factory cloud") and, for cloud, its credits.

**Credits.**

- The company's balance is in credits. Plans include a monthly amount (`saas-billing`: Pro and Team), extra packs
  can be bought, auto top-up optional. Included credits are used first and expire monthly; bought ones last 12
  months.
- **Pricing of credits:** a credit has a retail value (e.g. 100 credits = $1 shown to the customer); a cloud run
  costs (our compute + tokens at our cost) × markup (e.g. 1.33), converted to credits. So "$20 of cloud work
  included" in Pro costs us about $15, which is exactly the selling point the owner described: the plan looks
  worth more than its price, and every credit spent still covers our cost.
- **Per-agent view (the payroll idea, kept as a view, not as money):** Costs → Cloud shows what each agent spent:
  "Thread PO: 320 credits today, 41 min of work, 1.2M tokens". Limits: a cloud budget per team per day or month
  and one per company per month; reaching one applies the team's "where it works" rule (overflow waits for your
  machines, cloud-only waits with an escalation to raise it).
- **Low balance:** a warning at 20 % and at zero; at zero, cloud work waits ("no credits left"); own machines
  keep working.

**Naming (owner to choose).** The unit must not be confused with the subscription price, yet should sell:

  | Option | Unit | Cloud product | Per-agent view | Sales line |
  |---|---|---|---|---|
  | A (recommended) | credits | App Factory Cloud | "cloud spend" | "Pro $29: 5 connectors + 2,000 cloud credits" |
  | B | agent hours (≈ an hour of one agent working; tokens converted) | Cloud crew | "hours worked" | "Pro $29: 20 agent hours included" |
  | C | payroll / salary (metaphor) | Cloud office | "salary" | "Pro $29: $20 payroll for your cloud crew" |

  A is the least confusing next to "$29/month" because credits are clearly a separate unit; B is the most
  tangible for a buyer but an "hour" with very different token use is hard to keep honest; C is the most fun but
  "salary" next to a subscription price is what could confuse. A recommended, with C's language used only in the
  per-agent view if wanted.

### Metering and our cost

- Metering per run: Sprite active seconds (from the run's start to its end, plus resume time) and tokens from the
  runtimes' usage reports (already collected). Idle Sprite time between runs is ours (it is short because of
  suspension) and is priced into the margin.
- A daily reconciliation compares the Fly and model provider invoices with the ledger; the margin covers the
  difference, failed runs and storage.

## Decisions needed

1. Naming: A credits (recommended), B agent hours or C payroll.
2. The markup (proposed ×1.33, so included "$20" costs about $15) and the retail value of a credit.
3. Our keys by default with bring-your-own as an option (proposed), or bring-your-own only at the start (no token
   risk for us)?
4. Packs, auto top-up, expiry of bought credits (proposed 12 months).
5. Which Sprite sizes to offer and the idle time before suspension (proposed 10 minutes).
6. The default "where a team works" for a company that has both (proposed: your machines only, overflow opt-in).

## Impact

All virtual node code lives in the virtual nodes manager (`internal/vnodes`, `cmd/factory-vnodes`, its own storage and
API: provider client, lifecycle, credits, cloud budgets, "where a team works"); for Core a virtual node is an ordinary
node (`design.md` D1, owner 2026-11-12). Core gains only general node capabilities that serve every node: a service
account for node administration, a queue read, node pause with a reason, node labels, team placement on nodes if
missing (`design.md` D4). Web: Nodes → "Run in App Factory cloud", Costs → Cloud, credit packs, cloud budgets and
"where a team works" through the manager's API; setup wizard choice. `saas-billing` (packs through the same provider,
credits included in plans), docs.

## Decisions (2026-11-06)

- **Virtual nodes.** Hosted machines are called virtual nodes, and the product calls every machine a **node** (the
  word "connector" goes away, see `nodes-naming`). A virtual node is created in one click from the web.
- **Two kinds of virtual node:**
  - with **our model token**: the company pays its model usage and machine time in credits (billing as above);
  - with the **company's own token** (bring your own key or subscription login): only machine time is paid in
    credits, model usage is the company's own.
- Open: the credit naming is now only a billing word (credits proposed); markup, packs and expiry, sizes and idle
  time, and the default "where a team works" stay as proposed until decided; the Sprites spike comes first.

## Remaining decisions taken by Claude at the owner's request (2026-11-06)

- **Credits** are the unit; retail markup ×1.33 over our cost (so 2,000 included credits ≈ $20 retail ≈ $15 cost).
- **Order:** virtual nodes with the company's own token first (only machine time in credits: no model-cost risk for
  us), then with our token (model usage in credits) once metering is proven.
- **Packs:** $10, $25, $50; optional auto top-up when the balance falls under a threshold; bought credits expire
  after 12 months, included monthly credits at the end of the month.
- **Sizes:** Small (2 vCPU, 4 GB) and Large (4 vCPU, 8 GB, for builds and Blender); suspended after 10 minutes idle.
- **Default placement:** a team works on its own nodes only; overflow to virtual nodes is an explicit opt-in per team.
- All of it is checked against the Fly.io Sprites spike (0.2) and may change with what it shows.
