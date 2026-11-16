## 1. Tool table

- [ ] 1.1 `internal/node/tools.go`: one entry per tool (command, version args, minimum or exact pin, feature,
      platforms, reason, fix per platform).
- [ ] 1.2 Determine and record the minimums: git, gh, bwrap and socat from the invocations in the code (oldest version
      supporting each, with the release note); gitleaks 8.21, Node.js 22, Codex 0.162.1, Claude Code from the lock.
- [ ] 1.3 One parser and comparison (`readiness.FindVersion`, `CompareVersions`); remove the gitleaks, Node.js and
      install.sh copies.
- [ ] 1.4 Test: every external command the node runs (git, gh, gitleaks, bwrap, socat, xcrun) has a tool entry.

## 2. Effect

- [ ] 2.1 A failing tool is a `FaultTool` readiness fault of its feature: `worktrees`/`forge` → not ready for
      repository projects; `sandbox` and driver tools → driver off; `xcode` → no `xcode` label.
- [ ] 2.2 Re-check every 10 minutes and on Check again, as readiness.
- [ ] 2.3 doctor prints the same results; install.sh keeps only its pre-download checks and runs doctor.
- [ ] 2.4 Tests with fixtures: too old, no version, missing, passing; a node with failing `worktrees` still gets runs
      without a repository.

## 3. Report

- [ ] 3.1 `tools` in register and health; storage and API.
- [ ] 3.2 Nodes card, `factory nodes list`, `factory_nodes`.

## 4. Core-raised minimums (if accepted)

- [ ] 4.1 `tool_minimums` in the heartbeat answer from `FACTORY_NODE_TOOL_MINIMUMS`; never lower than the node's own.
- [ ] 4.2 The node applies it and re-checks at once; test with a raised git minimum.

## 5. Docs

- [ ] 5.1 node-install.md: the tool table (generated or checked by a test against `tools.go`), what fails and how
      to fix it per platform.
