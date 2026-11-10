Status: implemented 2026-11-10 (approved by the owner 2026-11-10; proposed 2026-11-07 after connecting two new nodes, a Hetzner Linux server and a second
MacBook). See "Implementation (2026-11-10)" for what was decided beyond the proposal.

## Why

On 2026-11-07 the owner connected `ubuntu-8gb-hel1-1` (Linux, root, user systemd unit) and `MacBook-Air-Juliya`.
Within an hour three tasks of the game project ran out of retries and escalated to the owner, and the board showed
them as "Escalated to you", although nothing was wrong with the tasks:

| What happened | Cause |
|---|---|
| bb04523f, 35a0a25e, 118cccd0: `retries_exhausted`, "last run setup_failed: git clone … Repository not found" (9 runs) | The new nodes had no GitHub access (no `gh auth` on Linux; the second Mac's account had no access to the private repo). Core kept offering the project's runs to them. |
| 727a3193: two runs `failed` on Linux, "Native CLI binary for linux-x64 not found" | The packaged node's Claude runs did not get the Claude Code binary next to the package; the probe did, so the node showed Claude "enabled". |
| 35a0a25e: two review runs `failed`, "--dangerously-skip-permissions cannot be used with root/sudo privileges" | The node was installed as root; Claude Code refuses to run agents as root. install.sh supports `--os-user`, but nothing warns or refuses. |
| Linux node would stop when the owner logs out | install.sh enabled a user unit for root without `loginctl enable-linger`. |
| `git.user` failed on the Linux node (and on the owner's Mac service) | No git identity for the node's user; commits need one. |
| Agents could not run `godot` or `openspec` on Linux | Nothing tells Core which tools a project needs; the node took the work anyway. |
| 2026-11-08, `hetzner-hel1` (reinstalled under `--os-user factory`): game-qa, gameplay-dev and ui-dev runs failed "Sandbox required but unavailable: bubblewrap (bwrap) not installed, socat not installed"; three tasks ran out of retries and escalated to the owner | Claude Code's sandbox (`sandbox: worktree`) needs bwrap and socat on Linux, and on Ubuntu 23.10+ an AppArmor rule letting bwrap create user namespaces (`kernel.apparmor_restrict_unprivileged_userns=1`). Neither the installer nor the driver probe checks it, so the node reported claude OK and took work. |

Two defects in how Core treats this:

1. **A node's environment failure counts against the task.** `setup_failed` before the agent starts (clone, auth,
   missing runtime binary) is the node's problem, but it spends the task's retries and escalates the task to the
   owner, while the node stays eligible and keeps failing the next task the same way.
2. **Core does not know what a project needs from a node.** It offers a run to any node with the agent's runtime,
   even when the node cannot reach the repository or lacks the project's tools.

## What Changes

- **Node failures charge the node, not the task.** A run that ends `setup_failed` with a node-side cause (repository
  access, worktree, runtime binary, missing tool) does not count toward the task's retries; the task is offered to
  another node at once. The node gets a per-project readiness fault ("cannot clone ivishnevs/gnomys: Repository not
  found") shown on its card with the fix (`gh auth login`, share the repo), and gets no runs of that project until it
  passes the check again (re-probed on its next heartbeat after a change, or by "Check again"). If no ready node is
  left, the task waits with reason `no_ready_node` naming the nodes and their faults, instead of escalating.
- **Project requirements.** A project may declare the tools its runs need (`requires: [godot 4.7.2, openspec, gh]`,
  each a command and a version check). Nodes probe them per project with `forge.repo` (clone access) and report them;
  Core offers a project's runs only to nodes that meet them. The Nodes page shows per project "ready" or what is
  missing.
- **Installer on Linux.** Run as root without `--os-user`, install.sh stops and offers `--os-user factory` (Claude
  Code does not run agents as root; agents should not have root on the host anyway); the doctor fails the Claude
  driver on a root node with that hint. For a user unit, install.sh enables linger (or tells how); it checks the git identity and
  asks for it; it reports the Node.js version when a project requirement needs a newer one.
- **Sandbox dependencies on Linux.** The Claude driver probe runs the sandbox once (bwrap with a user namespace,
  socat) when the node's sandbox is on; on failure the driver is not OK, with the cause and the fix (install
  `bubblewrap socat`; on Ubuntu with restricted user namespaces, the AppArmor profile for `/usr/bin/bwrap`). The
  installer checks the same and prints the commands (it does not change the host's security settings itself).
- **Pausing a node that holds sessions asks what to do with them.** Pausing a node shows the session leases it holds
  (session-affinity) and the runs waiting for them, and asks: release them now (the runs go to another node and start
  a fresh session from Core's context), or keep them until the release timeout (default 90 min, for a short pause).
  A run queued with reason `affinity` behind a paused node shows on the task and the board as "waits for <node>
  (paused) until HH:MM" with a "Release now" action, and the owner gets one notice per pause naming those tasks. Seen
  2026-11-07: pausing MacBook-Air-Juliya held network-dev's run of 727a3193 for 90 minutes while the owner's Mac had
  free slots, and network-dev's whole queue (the playtest server deploy) waited behind it; the board showed the task
  under To do although it had a queued run.
- **Escalations name the node.** When a node fault does reach the owner (every node failed), the question names the
  node and the fix, and `retry` is offered only after a node passed its check again.

Not in scope: installing project tools automatically on nodes, per-node labels for macOS/iOS work (separate).

## Done

- 2026-11-07, `2d8c15c`: the packaged Claude shim gives runs the Claude Code binary next to the package (regression
  test in workers/claude). On the owner's Linux node the path was also set in `node.yaml` by hand.

## Implementation (2026-11-10)

Decided while implementing, beyond the text above:

- **No escalation for `no_ready_node`.** The task waits (task reason and queued reason `no_ready_node`, per node
  `not_ready`), never parks; the owner learns it through a `no_ready_node` inbox alert (task_alerts, synced by the
  sweep, cleared when a node is ready) and the `node.not_ready` attention event, both naming the nodes and fixes.
  Nothing needs a `retry` answer: the queued run goes to the first node that passes its check. "Escalations name the
  node" applies to `budget_exhausted`, the only retry escalation a node fault can still reach.
- **Node faults count against the daily budget** (not attempts, not backoff): the guard against a node whose check
  passes while its runs keep failing (e.g. a runtime fault the project check cannot see).
- **Classification.** The node classifies its own ends (`readiness.Classify`, `Event.fault`) and sends `setup_failed`;
  Core classifies the texts older nodes send. A `failed` run is a node fault only for runtime start refusals (root,
  native binary, sandbox) and only when the runtime used no tokens; repository and worktree texts only for
  `setup_failed`. `workspace_diverged` stays the task's. A node-fault run of a side-effect task is not inspected.
- **Check protocol.** Core lists every live project with a repository or `requires` in the heartbeat answer
  (`checks`, with a generation per node and project); the node checks again when the generation or contents change
  and reports `readiness` in every heartbeat. A run's fault sets a minimum generation, so a result from before the
  fault never clears it. Repository and tool faults are re-checked at once; runtime, sandbox and worktree faults
  after 10 minutes (`ReadinessRecheck`); every fault again every 10 minutes; Check again at once. A node that never
  reports readiness (an older version) is unknown, so ready; Check again clears its faults. Runs also check the
  project's tools before the runtime starts (the claim answer carries `requires`), as the node's own fault.
- **`requires` format:** `"godot 4.7.2"` or `{"command", "version", "version_args"}`; a command is a bare name on
  PATH, versions dotted numbers, at most 32; set on create and update (`requires`, `[]` clears).
- **Session holders not ready for the run's project** count as inactive (release timer). A released lock now stays
  in `session_locks` until the holder is active again or another node takes the session (deleting it on release
  restarted the timer at the next look, which would have undone "Release now").
- **Pause API** for the web: `GET /nodes/{id}/sessions`, `POST /nodes/{id}/pause {reason, sessions}`,
  `POST /nodes/{id}/sessions/release` (`{run_id}` for one run), `POST /nodes/{id}/check` (all under `/nodes` so the
  web's core proxy reaches them). The notice is one `node.paused_sessions` feed event per pause. On the board a
  queued run held back by an inactive holder or `no_ready_node` files the task under Blocked with "Waits for NODE
  (paused) until HH:MM"; "Release now" is on the task page and the node card (a card is a link).
- Tasks 3.1 and 3.2 (installer, `claude.root`, `claude.sandbox`) as described in docs/node-install.md; the root
  refusal has no override flag.
- 5.1 is covered by `TestNodeReadinessEndToEnd` (real Core and nodes over signed HTTP) and the storage tests in
  `internal/storage/readiness_test.go`; not run on a live node.

## Impact

`internal/node` (doctor: per-project `forge.repo`, requirement probes, readiness in heartbeats), Core claim and
retry accounting (`setup_failed` causes, `no_ready_node` wait), project config (`requires`), install.sh (linger, git
identity), web Nodes page and escalation texts. Specs: `run-recovery`. Related: `node-packaging-updates`,
`node-runtime-accounts`, `connector-trust`.
