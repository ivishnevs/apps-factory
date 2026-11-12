## Context

Virtual nodes are machines App Factory runs for a company (Fly.io Sprites, see the spike 0.2). The proposal lists
what they do for the customer (one-click creation, sleep when idle, credits, cloud budgets, "where a team works");
`node-identity` D8 fixed that a virtual node uses the same keys and signatures as any node and is enrolled through
its provisioning channel. This design fixes where the code lives. Decided by the owner on 2026-11-12.

## Goals / Non-Goals

**Goals**
- All management and configuration of virtual nodes is isolated in one component, the virtual nodes manager.
- The rest of the application is not touched: for Core a virtual node is an ordinary node.

**Non-Goals**
- Changing the dispatcher, node ties, the run lifecycle or the cost ledger of Core for virtual nodes.
- A second node protocol or transport for virtual nodes.

## Decisions

### D1. The gate: Core does not know about virtual nodes (owner, 2026-11-12)

This is the acceptance condition of the whole change and is checked in review of every task:

- Core (`internal/core`, `internal/storage` of Core, the dispatcher, node ties, runs, costs) has no notion of a
  virtual node, a Sprite, a provider, credits or a cloud budget. No `virtual`, `cloud`, `sprite` or `credit`
  branch, column, table or wait reason appears there.
- A virtual node runs the same `factory-node` release as a customer's machine, enrolls with the same keys and
  signatures (`node-identity`), claims runs over the same API and receives secrets sealed the same way.
- Whatever Core must gain for this change is a **general node capability**, useful to every company and every node
  (own or virtual), named and tested without reference to virtual nodes (see D4).
- Removing the manager leaves a working App Factory: its virtual nodes look like nodes that went offline.

### D2. The virtual nodes manager

A separate component, `internal/vnodes`, run as its own process `cmd/factory-vnodes` (one deployment next to Core,
serving every company). It owns:

- the provider client (Fly Sprites: create, resume, suspend, destroy) behind an interface, so the provider can be
  replaced and tests run against a fake;
- its own storage (own schema or database): virtual nodes per company, size, state (creating, running, sleeping,
  destroyed), the Core node id it maps to, credit balance and entries, cloud budgets, "where a team works";
- the lifecycle loop: wake, sleep, budget enforcement, reconciliation with provider invoices;
- its own API for the web (create a virtual node, sizes, balance, packs, budgets, Costs → Cloud).

It talks to Core only through Core's public API, as a company admin would, with a service account (D4.1). It never
reads or writes Core's database.

### D3. How the manager does each job with ordinary node mechanics

| Job | How |
|---|---|
| Create | Issue an enrollment code through the API, create the Sprite with the code in its bootstrap, read the fingerprint from the machine it created and approve it with the ordinary approve call. |
| Wake | Nodes claim work, so a sleeping Sprite claims nothing. The manager polls the company's queue (D4.2): work that the team may run on the virtual node and no online node takes → resume the Sprite. Work tied to the virtual node (`node-ties`: pinned worktree or session affinity) always wakes it. |
| Sleep | No runs on the node for the idle time (10 min) → pause the node (D4.3), check again that nothing was claimed, suspend the Sprite. |
| Credits and budgets | The manager meters active seconds itself (it switches the Sprite on and off) and keeps the ledger. No credits or a used-up budget → it does not wake the node and keeps it paused with the reason; work waits for the company's own nodes with no logic in the dispatcher. |
| "Where a team works" | Expressed through the team's allowed nodes (general placement of a team on a set of nodes, D4.5), which the manager sets; "overflow" is the manager waking the virtual node only when own nodes are busy or offline. |
| Tokens (our key, second stage) | Read the usage of runs on its nodes through Core's costs API and book it as credits in its own ledger. |
| Display | The node carries a label ("App Factory cloud") through the general node labels (D4.4); the web shows cloud details from the manager's API. |

### D4. General node capabilities Core may gain

Each one is a separate task, checked first against what Core already has; each one serves own nodes too.

1. **Service account for node administration:** a company-scoped credential that may issue enrollment codes,
   approve, pause and revoke nodes and read the queue, without an owner or admin person.
2. **Queue read:** for a company (and team), the claimable work no online node takes, with the nodes it is tied to.
3. **Node pause with a reason (drain):** a paused node claims nothing new; the reason is shown as the wait reason of
   work that waits for it ("cloud budget used up for today" is just the text the manager sets). Closes the race
   between suspending a Sprite and a claim in the same moment.
4. **Node labels:** free-form labels set at enrollment or by an admin, shown on the Nodes page and on runs.
5. **Team placement on nodes** if `node-ties` / team settings do not already allow restricting a team to a set of
   nodes.

### D5. The web

"Run in App Factory cloud" on Nodes and in the setup wizard, Costs → Cloud, credit packs, cloud budgets and "where a
team works" call the manager's API. Existing pages keep working without the manager (the cloud parts are hidden when
it is not deployed).

### D6. Subscription logins on virtual nodes (proposed 2026-11-13, open)

A virtual node with the company's own token may run on a person's Claude or Codex subscription. On an own node this
is `factory-node login claude|codex`: the runtime's own login flow in a terminal, writing the node's own config
directory, with the account recorded and changes paused (`node-runtime-accounts`). A virtual node has no terminal
the person sits at, so:

1. **Login relayed through the web (proposed default).** On the virtual node's page, "Log in to Claude / Codex":
   the manager runs the same `factory-node login RUNTIME` in the Sprite (provider exec, in a PTY), shows the login
   link it prints and passes back what the person enters (Claude: the code pasted after authorizing; Codex: its
   device-code flow). The credentials stay on the Sprite's persistent disk, the runtime refreshes them itself, the
   account is recorded as on any node. Core is not involved (D1). The manager never stores or logs the link or the
   code.
2. **Long-lived token as a company secret (alternative):** e.g. a Claude token made by `claude setup-token` on the
   person's machine, stored as a company secret and handed to the runtime by the node. Works on own servers too, so
   it would be a general node capability (D4), not a virtual-node one.
3. **API key as a company secret** — already in the plan (2.5); no subscription involved.

Copying a login file from the person's machine (as `-copy-current` does for Codex) is not offered: refresh tokens
rotate, and two machines with one copy log each other out.

Rules: only an owner or admin starts a login; one account per runtime per virtual node (a second person's
subscription is a second virtual node); a lost or expired login shows on the node like on any node ("no Claude
login") and the web offers "Log in again"; the subscription's quota is shared with the person's own use and handled
by the existing quota pauses.

To check before building (spike 0.2 and 0.4): the headless login flows of both runtimes in a Sprite, and whether the
providers' terms allow a person's subscription on a machine we host for their company. If they do not, virtual nodes
run on API keys only (option 3) and the setup wizard says so.

## Risks / Trade-offs

- **Sleeping looks like offline.** A pinned task waits for its node past the pin timeout and escalates
  (`pinned_node_unavailable`); session affinity is released 90 min after the node is inactive. Mitigation: the
  manager wakes the node for any work tied to it (D3), so a sleeping node with tied claimable work wakes in seconds.
  To be confirmed by the spike (resume time). Teaching Core a "sleeping" state is not an option (D1).
- **Polling delay:** wake latency = poll interval + resume time. If too slow, the fix is a general event stream of
  the queue in Core (D4.2), not a virtual-node hook.
- **Double accounting:** compute seconds are in the manager, tokens in Core; the daily reconciliation compares both
  with provider invoices.
