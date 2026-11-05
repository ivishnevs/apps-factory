Status: proposal — needs the owner's decisions (section "Decisions needed") and a technical spike on Fly.io Sprites.
Nothing is implemented. Raised by the owner on 2026-11-05.

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

### Paying for usage: Payroll

Agents already behave like employees (roles, managers, a team), so their cost is shown the same way: **payroll**.

- **Company wallet in credits.** The company tops up credits (1 credit = $0.01, packs e.g. $20 / $50 / $200 with
  optional auto top-up). Cloud work draws from the wallet; one's own machine draws nothing.
- **Each agent earns.** Every run on a cloud connector is priced at our cost of compute (Sprite seconds by size)
  plus tokens at the provider's list price, plus the margin, and is booked to the agent who ran it: the agent's
  "pay" for the day. Costs shows "Thread PO earned $3.20 today: 41 min of work, 1.2M tokens".
- **Salary caps.** Each agent (or team) can get a daily or monthly salary cap; reaching it parks the agent's new
  work with a reason ("Thread PO reached the salary cap of $10 today") exactly like a quota pause, and the owner
  can raise it from the escalation. A company-wide monthly budget caps everything.
- **Low balance:** a warning at 20 % and at zero; at zero, cloud work waits ("the wallet is empty") and nothing is
  lost; the owner's own machines keep working.
- **Bonuses** (the creative part, optional): a monthly subscription includes a monthly "salary fund" of credits
  (e.g. Pro includes $10 of cloud work), so trying the cloud needs no top-up; unused fund does not roll over.
- The cost ledger already records tokens and dollars per run and agent; it gains compute seconds, the price with
  margin and the wallet entry, so the customer sees exactly what they paid for.

### Metering and our cost

- Metering per run: Sprite active seconds (from the run's start to its end, plus resume time) and tokens from the
  runtimes' usage reports (already collected). Idle Sprite time between runs is ours (it is short because of
  suspension) and is priced into the margin.
- A daily reconciliation compares the Fly and model provider invoices with the ledger; the margin covers the
  difference, failed runs and storage.

## Decisions needed

1. Naming in the product: "Payroll" and "salary caps" (proposed), or plain "credits" and "spending limits"?
2. Margin: one percentage for compute and tokens (e.g. 20 %), or different ones?
3. Our keys by default with bring-your-own as an option (proposed), or bring-your-own only at the start (no token
   risk for us)?
4. Top-up packs and auto top-up; whether plans include a monthly salary fund and how much.
5. Which Sprite sizes to offer and the idle time before suspension (proposed 10 minutes).
6. Credits need the payment provider from `saas-billing`: same provider for one-off top-ups.

## Impact

New `internal/cloud` (Fly Sprites client: create, resume, suspend, destroy; node bootstrap with pre-approved keys),
`internal/storage` (cloud connectors, wallet and its entries, salary caps), the dispatcher (wallet and salary cap
as wait reasons, resume a suspended Sprite for claimable work), the cost ledger (compute seconds, priced amount),
web (Connectors: "Run in App Factory cloud", Costs → Payroll, wallet top-up, caps on agent and team pages, setup
wizard choice), `saas-billing` (top-ups through the same provider, plans' salary fund), docs.
