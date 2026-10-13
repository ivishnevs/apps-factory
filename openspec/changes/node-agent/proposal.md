## Why
Today a worker process executes one run at a time, so parallelism means starting several unrelated processes per machine, each with its own NATS connection and no shared view of the machine. Nothing limits heavy work per machine, cleans the disk or stops agents from driving the host's input devices. Risks on a shared machine: several simulators and builds at once overload it until agents cannot complete simple UI steps; the disk fills up with build caches, simulators and worktrees of closed tasks; and agents must not drive host input devices through host-input tools (`cliclick`, AppleScript), which interferes with anyone using the machine.

## What Changes
- **One node process per machine** (`cmd/factory-node`, launchd/systemd) with **N worker slots** (goroutines): one Core connection, one outbound NATS connection (TLS/WebSocket, per-node credentials), a per-node control subject for stops, registration with a boot epoch, and a node heartbeat that carries the inventory of runs and machine health, renews all their leases and returns the runs to stop (revoked, terminal, timed out, unknown to Core). A database restore starts a new server incarnation; nodes re-register.
- **Resource classes per node** (e.g. `simulator: 2`, `build: 2`); agents declare needs and labels; **Core grants a claim only when the node has a free slot and capacity**; capacity is released when the run has ended and its worktree is sealed. Pinned worktrees (`task-worktrees`) are enforced at claim.
- **Janitor** driven by template hooks: remove worktrees of closed tasks after seal, run per-stack cleanup (simulators tagged with the task, DerivedData; emulators, Gradle caches), prune caches by age; **low-disk alert** and aggressive cleanup below a threshold; stop claiming below a critical threshold.
- **Node health control**: launchd/systemd keeps the node process alive (KeepAlive) and Core alerts once on a restart loop (several new epochs in a short window); an internal watchdog checks slot consistency (a slot with no agent process or a process with no slot), reaps orphans and verifies that heartbeats actually leave; periodic self-checks (git/forge credentials, Core and NATS connectivity, free disk, stack checks via template hooks such as Xcode/simulators, plus runtime probes provided by drivers) raise one alert and pause the node only when it cannot work at all; `factory node doctor` runs all checks on demand.
- **Node health in the event feed**: registered, offline/online, low disk, critical disk, paused, restart loop, failed check, stalled heartbeat.
- **Host UI is forbidden by policy**: command-level access rules become refusing shims first on the agent's PATH (and driver deny hooks in `runtime-drivers`); approval-required commands pass once with an exact single-use grant. The owner CLI is denied the same way.

## Out of scope
Cloud worker pools, NATS leaf nodes, relocation of tasks between nodes, OS-level isolation, mid-run resource requests.

## Dependencies
`postgres-tenancy`, `trigger-log-scheduling` (claim reasons), `reliable-recovery` (epoch, liveness, outcomes), `task-worktrees` (seal, pinning).

## Capabilities
### New Capabilities
- `node-agent`: Node process with slots, registration and inventory heartbeat, resource classes at claim, janitor hooks, disk thresholds, node health events and command-level guards.

### Modified Capabilities
None.

## Impact
New `cmd/factory-node`, `internal/node` (slots, inventory, janitor, shims), `internal/worker` (becomes the slot executor), `internal/core` (node registry, node heartbeat, claim capacity), `internal/storage` (`nodes`, `node_resources`, run `node_id`), `internal/config` (agent `resources`/`labels`, `NodeHooks` kind), `internal/policies` (`commands` in access rules), `configs/policies/host-ui.yaml`, docs `architecture.md`, `development.md`, `policies.md`.
