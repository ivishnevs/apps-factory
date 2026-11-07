## 1. Node failures

- [ ] 1.1 Classify `setup_failed` causes (repository access, worktree, runtime binary, missing tool) as node-side;
      such runs do not count toward the task's retries and the task is re-offered to another node; tests.
- [ ] 1.2 Per-project readiness fault on the node (cause, fix hint), no runs of that project until the node passes
      its check again; `no_ready_node` wait reason; escalation only when no node is ready, naming nodes and fixes.

## 2. Project requirements

- [ ] 2.1 `requires` on the project (command, version check); node probes per project incl. `forge.repo`; Core offers
      runs only to ready nodes; Nodes page shows per-project readiness.

## 3. Installer

- [ ] 3.1 install.sh on Linux: enable linger for user units; git identity check and prompt; Node.js version hint.

## 4. Verify

- [ ] 4.1 Sandbox: a node without repository access gets a readiness fault and no runs, the task runs on another node
      with its retries untouched; with only that node, the task waits `no_ready_node` and does not escalate before
      the fault is fixed.
