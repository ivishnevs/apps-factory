Gate for every task (`design.md` D1, owner 2026-11-12): all virtual node code is in the virtual nodes manager; Core
has no notion of a virtual node, provider, credit or cloud budget, and a virtual node is an ordinary node for it.
Core changes are only the general node capabilities of section 1.

## 0. Decisions and spike

- [x] 0.1 Credits ×1.33; own-token virtual nodes first, our token after; packs $10/25/50, auto top-up, 12-month expiry; Small/Large, 10 min idle; own nodes only by default (decided by Claude at the owner's request 2026-11-06).
- [x] 0.3 Architecture: isolated virtual nodes manager, Core untouched (owner 2026-11-12, `design.md`).
- [ ] 0.2 Spike: Fly.io Sprites API (create, suspend and resume times, disk persistence, pricing per second, limits,
  networking to Core); a `factory-node` image runs in a Sprite, enrolls through its bootstrap and claims a task;
  resume time against pin and affinity timeouts (`design.md` risks). Result written into `design.md`.

## 1. General node capabilities in Core (each checked first against what exists; none mentions virtual nodes)

- [ ] 1.1 Service account for node administration: issue enrollment codes, approve, pause, revoke nodes, read the
  queue; company-scoped; tests.
- [ ] 1.2 Queue read: claimable work no online node takes, per company and team, with the nodes it is tied to; tests.
- [ ] 1.3 Node pause with a reason (drain): a paused node claims nothing new; the reason is the wait reason of work
  waiting for it; web shows it on Nodes; tests.
- [ ] 1.4 Node labels: set at enrollment or by an admin, shown on Nodes and runs; tests.
- [ ] 1.5 Team placement on a set of nodes, if `node-ties` / team settings do not cover it; tests.

## 2. Virtual nodes manager (`internal/vnodes`, `cmd/factory-vnodes`)

- [ ] 2.1 Provider interface and Sprites client; fake provider for tests.
- [ ] 2.2 Own storage: virtual nodes, size, state, Core node id; migrations; tests.
- [ ] 2.3 Create: enrollment code into the bootstrap, fingerprint check and approve through the service account,
  label; destroy with revoke; tests against the fake provider and a test Core.
- [ ] 2.4 Lifecycle loop: wake for claimable or tied work, pause then suspend after idle, no race with claims; tests.
- [ ] 2.5 Model access: the company's API key secret (first); our keys with per-company metering (second stage).
- [ ] 2.6 Manager API for the web; deployment next to Core; docs.

## 3. Credits and cloud budgets (in the manager)

- [ ] 3.1 Storage: credit balance and entries (included monthly, bought, run charge, refund, expiry), cloud budgets
  per team and company, "where a team works"; tests.
- [ ] 3.2 Metering: active seconds from the lifecycle; tokens of runs on its nodes from Core's costs API (second
  stage); priced with the markup; daily reconciliation with Fly and model invoices; tests.
- [ ] 3.3 Enforcement: no credits or a used-up budget → the node stays paused with the reason and is not woken;
  "where a team works" through team placement (1.5) and overflow waking; tests.
- [ ] 3.4 Packs through the `saas-billing` provider; credits included in Pro (Team dropped 2026-11-06).

## 4. Web

- [ ] 4.1 Nodes → "Run in App Factory cloud" with sizes; the setup wizard offers it next to "Your machine"; hidden
  when the manager is not deployed; browser check.
- [ ] 4.2 Costs split into Your machines / Cloud; credit balance, packs and auto top-up; cloud budgets and "where a
  team works" on team pages; low-balance warnings; all cloud data from the manager's API; browser check.
- [ ] 4.3 Gate check: a test that Core packages contain no virtual-node, provider, credit or cloud-budget code.
