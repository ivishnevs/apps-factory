## 0. Decisions

- [ ] 0.1 Owner decides the default sandbox, isolated mode timing, open source and license, signing accounts and
  the pentest (proposal).

## 1. Confinement

- [ ] 1.1 Codex runs in `workspace-write`, Claude with sandboxed Bash and worktree-only file access; the connector's
  state and the home directory unreadable from runs; tests on macOS and Linux (an agent trying to read `~/.ssh`
  and the node keys fails).
- [ ] 1.2 Local policy in `node.yaml` (project folders, runtimes, full-access opt-in per project, network rule,
  slots); refusals reported to Core and shown in the web; tests.
- [ ] 1.3 `factory-node pause` (local, immediate) and `factory-node uninstall`; tests.
- [ ] 1.4 Linux installer: optional `appfactory` OS user.
- [ ] 1.5 Isolated mode (container or VM) as an option.

## 2. Verifiable

- [ ] 2.1 Local activity log and `factory-node activity`; `factory-node doctor --security`; Connectors security
  panel in the web.
- [ ] 2.2 Signed and notarized macOS binaries, signed Linux packages, SBOM in releases.
- [ ] 2.3 Connector source published.

## 3. Communicated

- [ ] 3.1 `docs/security.md` and a Security page on the landing; wording on the install and connect pages;
  security contact.
- [ ] 3.2 External penetration test before launch; summary published (`saas-release-readiness`).
