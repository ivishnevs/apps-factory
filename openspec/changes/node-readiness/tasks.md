## 1. Node failures

- [x] 1.1 Classify `setup_failed` causes (repository access, worktree, runtime binary, missing tool) as node-side;
      such runs do not count toward the task's retries and the task is re-offered to another node; tests.
- [x] 1.2 Per-project readiness fault on the node (cause, fix hint), no runs of that project until the node passes
      its check again; `no_ready_node` wait reason; escalation only when no node is ready, naming nodes and fixes.

## 2. Project requirements

- [x] 2.1 `requires` on the project (command, version check); node probes per project incl. `forge.repo`; Core offers
      runs only to ready nodes; Nodes page shows per-project readiness.

## 3. Installer

- [x] 3.1 install.sh on Linux: refuse root without `--os-user` (offer `--os-user factory`), doctor fails Claude on
      a root node; enable linger for user units; git identity check and prompt; Node.js version hint.
- [x] 3.2 Linux sandbox: Claude driver probe runs the sandbox (bwrap with a user namespace, socat) when the node's
      sandbox is on; failure disables the driver with cause and fix (packages; AppArmor profile for bwrap on Ubuntu
      23.10+); install.sh checks and prints the commands; tests.

## 4. Pause and session leases

- [x] 4.1 Pause shows held session leases and waiting runs, asks "release now" or "keep until timeout"; queued
      `affinity` runs behind a paused node show "waits for <node> until HH:MM" with "Release now"; one notice per
      pause; tests.

## 5. Verify

- [x] 5.1 Sandbox: a node without repository access gets a readiness fault and no runs, the task runs on another node
      with its retries untouched; with only that node, the task waits `no_ready_node` and does not escalate before
      the fault is fixed.
