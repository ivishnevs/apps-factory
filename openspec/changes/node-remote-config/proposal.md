Status: idea (raised 2026-11-10 by the owner: "the node's config could be set from the web / API and the node pulls
it; not sure whether that is good or bad — record it and defer"). Not scheduled. Nothing implemented.

## Why

A node's settings live in `node.yaml` on the machine (slots, drivers, worktrees, sandbox, local policy). Changing
one means a shell on that machine and a node restart. With work-and-conversation-runs the owner also wants to tune
`conversation_slots` per node, and nodes are spread over machines the owner does not sit at (a Hetzner server, a
second MacBook). The web already shows the nodes; it cannot change them.

There is a precedent for Core-to-node instructions: account accepts (node-runtime-accounts 3.3) reach the node in
its poll answer (`NodeWork.AcceptAccounts`) and are applied once.

## Idea

- **Two kinds of node settings.**
  - *Capacity and scheduling* — `slots`, `conversation_slots`, labels, maybe driver enable/disable and runtime
    versions to install. Safe to manage remotely: they change how much work the node takes, not what it may do.
  - *Trust and machine* — sandbox mode, `policy.full_access`, local policy, `projects_root`, paths, binaries, login
    directories, `env_allow`. They stay local only: connector-trust's rule is that the machine's person decides what
    runs on their machine, and a compromised Core or owner account must not be able to widen it.
- **Desired config in Core, pulled by the node.** Core keeps a versioned desired config per node (web and API edit
  it, owner or company admin only). The node gets the version in its poll/heartbeat answer, fetches it when newer,
  validates it, applies what can change live (slots) and reports the applied version and any refusal. The web shows
  "applied" / "pending" / "refused: <reason>".
- **Local wins.** `node.yaml` may pin a setting (`remote: false` per key, or a whole `remote_config: off`); a pinned
  or trust setting sent remotely is refused and reported. Remote values never widen local limits (e.g. a cap of
  `slots_max` in node.yaml).
- **Applied without restart where possible.** Slots change at the next claim; a driver change restarts only that
  driver's probe; anything needing a restart says so.
- **Audit.** Every change is a feed event with who changed what on which node.

## Open questions

1. Worth it now, or only once there are many nodes per company (SaaS customers' own nodes)?
2. Which settings are remote at all (start with slots and conversation slots only?).
3. Should a node's person be asked to confirm remote changes on their machine (connector-trust), or is the local
   opt-out enough?
4. Write the applied values back into `node.yaml` (one source on the machine), or keep a separate
   `<state_dir>/remote-config.json` layered over it (node.yaml stays what the person wrote)?

## Impact (if adopted)

Core: desired node config (table, API, web Nodes page editor, feed events); poll/heartbeat carries the version.
Node: fetch, validate, apply live, report; node.yaml pins. Specs: `node-agent`. Related: work-and-conversation-runs
(conversation slots), connector-trust (local policy), node-packaging-updates (runtime installs), node-readiness.
