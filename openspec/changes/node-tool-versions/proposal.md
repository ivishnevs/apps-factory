Status: proposed 2026-11-16, awaiting the owner. Raised by the owner after the GCP node's gitleaks ("we already check
the tools a node needs; globally we must also check their versions against the minimum compatible ones").

## Why

On 2026-11-16 the new GCP node (`gcp-us-central1`, Ubuntu 24.04) passed `factory-node doctor` with apt's gitleaks.
That build is older than the `gitleaks git` subcommand the pre-push scan runs and reports no version
("version is set by build process"). `worktrees.scan` only looked the binary up, so the node took work, and every push
of a task branch would have failed at the end of a run. We fixed this one tool (9b8ffaa: gitleaks ≥ 8.21 in doctor,
self-checks and install.sh), but the class is still open.

**What a node is checked for today, by mechanism:**

| Tool | Where | Version checked? | Effect when bad |
|---|---|---|---|
| Project tools (`requires: ["godot 4.7.2"]`) | `internal/readiness`, per project, from Core | minimum, dotted | node not ready for that project (no runs, `no_ready_node`) |
| Codex | driver probe | exact pin 0.162.1 | driver off |
| Claude Code | package lock (`claude.lock`) | pinned by the package | driver off |
| Node.js (repository-built Claude shim) | driver probe | ≥ 22, own parser | driver off |
| gitleaks | doctor, self-checks, install.sh | ≥ 8.21 since 9b8ffaa, own parser (the 3rd copy) | Fail check only |
| git | doctor, self-checks | printed, not compared | Fail only when missing |
| gh | doctor | presence and `auth status` only | Warn |
| bwrap, socat | Claude sandbox probe, install.sh | functional probe, no version | driver off |
| Xcode, simctl | doctor | presence, simulator count | Fail/Warn, `xcode` label |
| Catalog stack checks (`node-hooks`) | doctor, self-checks | whatever the command does (exit 0) | `stack.<name>` Fail |
| install.sh prerequisites | shell | own checks and texts | printed notes |

The gaps:

1. **The node's own tools have no minimums.** A tool that is present but too old passes, and the failure shows up as
   a failed run (the gitleaks case), not as a node that is not ready.
2. **Minimums live in four places with four parsers.** `readiness.FindVersion`/`CompareVersions`, the Claude probe's
   Node.js check, the new gitleaks check in `doctor.go` and install.sh's shell. Each has its own texts and its own
   idea of what failing means.
3. **A failing node tool has no consistent effect.** A failing project tool keeps the project's runs away from the node.
   A failing node tool (old gitleaks, missing gh) only colours a doctor line, and Core keeps offering work that needs
   it.
4. **Nobody can see the versions.** The Nodes page shows driver versions, not git, gh, gitleaks or bwrap, so "which
   node has the old git" needs SSH.
5. **A minimum discovered in the field needs a node release.** Raising one means shipping and rolling out a new
   `factory-node`.

## What Changes

### 1. One table of node tools

- **Every tool the node itself uses gets one entry** in a single table in `internal/node` (`tools.go`). Each entry has
  the command, the version arguments, the minimum version (or an exact pin for the runtimes), the feature it serves,
  the platforms, the reason for the minimum ("`gitleaks git` subcommand"), and the fix per platform (brew, the release
  binary, apt).
- **Features, not "the node", own tools:**
  - `worktrees`: git, gitleaks;
  - `forge`: gh;
  - `sandbox`: bwrap, socat (Linux);
  - `driver.claude`: Claude Code, plus Node.js for a repository-built shim;
  - `driver.codex`: Codex;
  - `xcode`: Xcode, simctl (macOS).
- **One parser and one comparison:** `readiness.FindVersion` and `CompareVersions` for the node's tools and the
  project `requires` alike. The copies in `doctor.go` (gitleaks), the Claude probe (Node.js) and install.sh go away.
- **Each minimum is the oldest version that supports every invocation in the code**, with the reason recorded in the
  entry. Known today: gitleaks 8.21, Node.js 22, Codex exactly 0.162.1, Claude Code from the lock. To determine:
  git (partial clone with `--filter`, `worktree add`, `restore --source --worktree`), gh (`pr create`, `pr merge`,
  `pr view` flags in use), bwrap and socat. A test fails when a new invocation is added without its tool entry.

### 2. Same effect as project requirements

- **A failing tool turns its feature off, in the same way as a failing project requirement.** It becomes a readiness
  fault (`FaultTool`, detail, fix), re-checked every 10 minutes and on Check again:
  - `worktrees` or `forge` failing: not ready for projects with a repository;
  - `sandbox` failing: the Claude driver is off (as today);
  - a driver's tool failing: that driver is off (as today);
  - `xcode` failing: no `xcode` label, so no runs of agents that need it.

  A node with a failing feature never takes work that needs it. It is not paused as a whole: its other work goes on.
- doctor shows the same results with the same texts; install.sh runs `factory-node doctor` and keeps only the
  pre-download checks of its own (curl, tar, sha256).

### 3. Versions are visible

- **The node reports each tool's version and status** in register and health (`tools: [{name, version, min,
  status}]`).
- The Nodes card lists them, with a failing tool's fix next to it.
- `factory_nodes` gives managers the same, and `factory nodes list` prints it.

### 4. Core can raise a minimum without a node release

- **Core can raise a node tool's minimum.** It sends `tool_minimums` in the heartbeat answer, set by
  `FACTORY_NODE_TOOL_MINIMUMS` or the code's table in Core. This works like `min_node_version`, and Core can only raise
  minimums, never lower them. The node applies the higher of its own and Core's minimum and re-checks at once.
- When a broken version is found in the field, every node gets the fault and the fix within a heartbeat.

Out of scope:
- installing or upgrading tools on nodes (the node says what and how; the person does it);
- maximum versions (except the runtimes' exact pins);
- catalog stack checks, which stay arbitrary commands;
- project `requires`, which keeps its format.

## Questions for the owner

1. **Core-raised minimums (section 4):** now, or later? Without them a new minimum needs a node release.
2. **gh:** today a missing or logged-out gh is only a Warn. Should it be a `forge` fault, so the node is not ready for
   projects that open PRs? Recommended.
3. **Older nodes:** a node without the `tools` report is "unknown, so ready", as with readiness. Is that the right
   call, or should Core require a node version with the report before giving such a node work?
