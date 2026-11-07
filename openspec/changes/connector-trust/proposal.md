Status: decided 2026-11-06 by the owner (see "Decisions 2026-11-06"); packaging and updates moved to
`node-packaging-updates`.
Nothing is implemented.

## Why

People will install the connector on their own Mac or server only if they believe it cannot be used to break into
that machine. "Trust us" is not enough; the product has to make the dangerous things impossible by construction,
let people check that themselves, and say plainly what remains.

An honest look at today (2026-11-05):

- **Already strong:** the connector only makes outgoing HTTPS requests to Core — it opens no port and has no remote
  shell; every request is signed with the node's own key (keys 0600 in its state directory, never leave the
  machine); a connection is approved by a person in the browser and can be revoked at once; company secrets reach
  the machine sealed to that node's key and only for the run that needs them; packages come with SHA256SUMS.
- **The real gap:** agents run as the same OS user with full access (Codex `danger-full-access`, Claude
  `bypassPermissions`). Whatever drives an agent — a task from Core, a prompt injection hidden in a repository, an
  issue or a web page, or an attacker who took over Core — can read the person's home directory (SSH keys, browser
  data, other projects, `.env` files) and run any command. Policies and MCP grants are guardrails, not a sandbox
  (`docs/policies.md`, `docs/approvals.md`). This is what a careful customer will find, and it must be fixed before
  asking them to trust the connector.

## What Changes (proposed)

### 1. Make break-ins impossible by construction

- **Sandboxed runs by default.** Every agent run is confined to its project worktree:
  - Codex in `workspace-write` (Seatbelt on macOS, Landlock/seccomp on Linux) instead of `danger-full-access`;
  - Claude with its sandboxed Bash and file permissions limited to the worktree instead of `bypassPermissions`;
  - the connector's own state (keys, sealed secrets, `node.yaml`) and the rest of the home directory are not
    readable from a run.
  Full access stays possible only as an explicit local opt-in per project in `node.yaml` (for example a project that
  must run a local database), shown in the web as "full access on <machine>".
- **Stronger isolation as an option ("isolated mode"):** runs in a container or lightweight VM on the machine
  (Docker or Podman on Linux, Apple's container framework or a Lima VM on macOS) with only the worktree mounted and
  optional network egress limits. Recommended for servers and for teams with strict policies.
- **A separate OS user on servers:** the Linux installer offers to create an `appfactory` user so the connector
  never runs as the person or root.
- **The machine has the last word (local policy Core cannot change):** `node.yaml` lists the folders projects may
  live in, the runtimes allowed, whether full access is ever allowed, the network rule and the maximum agent slots.
  A task asking for more is refused on the machine and the refusal is shown in the web. Core can never update the
  connector, change its policy or run a command outside a run.
- **Pause and remove locally, any time:** `factory-node pause` stops taking work at once without Core;
  `factory-node uninstall` removes the service, keys, worktrees it created and state, and tells Core.

### 2. Let people check, not just believe

- **A local log that stays on the machine:** every run with its task, project, runtime, sandbox mode, the commands
  run and files changed, kept by the connector (`factory-node activity`), independent of Core.
- **"What can this connector do?"** `factory-node doctor --security` prints the effective sandbox, allowed folders,
  network rule, OS user and whether any project has full access; the web shows the same on the Connectors page.
- **Open connector source** (the connector, the runtimes' wrappers and the installer; Core stays closed) so anyone
  can read what runs on their machine.
- **Signed and verifiable releases:** macOS binaries signed and notarized by Apple, Linux packages signed;
  checksums and an SBOM published; reproducible builds as a later step.

### 3. Say it plainly

- **A Security page on the landing and in the docs:** what the connector can and cannot do, the threat model in
  plain words (a compromised Core, a malicious repository, a stolen laptop), what we store, how to revoke and
  remove, and how to report a vulnerability (security contact, later a bug bounty).
- **In the product:** the install step and the connect page say "outgoing connections only, runs confined to your
  project folders, you can pause or remove it any time", with a link to the details.
- **Security testing by AI agents** (owner's decision): a red-team run against the sandbox Core and a connector
  before each release — the public routes, connector sign-in, invitations, and malicious repositories trying to
  escape the sandbox — with the cases kept as regression tests and a summary published.
- **No machine at all:** cloud connectors (`hosted-connectors`) for people who do not want anything installed.

## Decisions needed

1. Default sandbox: workspace-confined runs for everyone (proposed), with full access only as a local opt-in per
   project? (Some existing flows that touch files outside the worktree would need the opt-in.)
2. Isolated mode (container / VM): build it now as an option, or after the sandbox default?
3. Open the connector's source (proposed) — and under which license?
4. Signing: the owner has an Apple Developer account (2026-11-05). Binaries installed with curl are not
   quarantined, so signing is for trust now and required for a downloadable .pkg/.dmg and the Mac app later.
5. Decided 2026-11-05: the penetration test is done by AI agents (a security team of our own, repeated before
   releases, prompt-injection cases included); an independent test may follow for enterprise customers.

## Impact

`internal/node` and `cmd/factory-node` (local policy, sandbox settings per runtime, pause, uninstall, activity log,
doctor --security), the Codex driver and `workers/claude` (sandbox modes), the installer (OS user, isolated mode),
Core (refusals from the machine's policy, connector security summary), web (Connectors security panel, install and
connect wording, landing Security page), docs (`docs/security.md`), release tooling (signing, notarization, SBOM).

## Decisions (2026-11-06)

1. Default sandbox: runs confined to the task's worktree for everyone; full access only as a local opt-in per project
   in `node.yaml`.
2. Isolated mode (container / VM): after the sandbox default, not now.
3. The connector's source is published, under Apache-2.0 (or a similar permissive license).
4. Binaries are signed and notarized with the owner's Apple Developer account.
5. (2026-11-05) Penetration test by our own AI security team before releases.

Also raised: a node without runtime dependencies (no Node.js to install) and updates that reach every node — see
`node-packaging-updates`.

## Implementation (2026-11-07)

- 1.1 `sandbox: worktree`: Codex in workspace-write (writes in the worktree and its `.git`, network on); Claude with
  Claude Code's Bash sandbox (writes in the worktree, its `.git`, temp) and deny rules for `~/.ssh`, `~/.aws`,
  `~/.gnupg`, `~/.config/gcloud`, `~/.kube` and the node's key files. Codex cannot be denied reads (runtime limit).
- 1.2 node.yaml `policy`: `projects`, `runtimes` (other drivers do not start, so no work is offered), `full_access`;
  a refused run ends `setup_failed` with "refused by this node's local policy" (on the run page) and in the activity
  log. `network` is accepted and reserved.
- 1.3 `factory-node pause/resume` (local, Core cannot lift it) and `uninstall`. 1.4 `install.sh --os-user NAME` on
  Linux. 2.1 local activity log, `factory-node doctor -security`, the node's security summary in its heartbeat and
  "Security on this machine" on the Nodes page. 2.2 `SBOM.json` per package. 3.1 `docs/security.md`, the public
  `/security` page linked from the landing and the install steps.
- Decided during implementation: existing nodes keep `sandbox: full` until their owner switches (a silent switch
  could break tools a running team uses outside the worktree); new installs get `worktree`.
- Waiting for the owner: 1.5 isolated mode (decided "later"), signing and notarization (Apple developer account),
  publishing the source (a public action: the owner's go), the security contact address (3.1), the penetration test
  budget (3.2).
