## 1. Own work first

- [ ] 1.1 Poll: order the offered runs pinned to the polling node first, then runs whose session it holds, then the
      rest by creation; all existing checks unchanged; tests (a full node frees a slot and takes its pinned run before
      older unpinned runs; other nodes still skip it).

## 2. Visibility

- [ ] 2.1 Task verdict: a pinned task whose node is online but full waits as `pinned_node` naming the node and its
      runs; queued runs waiting for their session holder name it too; web and TUI texts.
- [ ] 2.2 Nodes page: per node, the pinned tasks and held sessions with the runs waiting for them; pin badge on task
      cards.

## 3. Verify

- [ ] 3.1 Replay 2026-11-08 on a sandbox: with VP-3 pinned to a full node and two older unpinned network-dev runs
      queued, the node takes VP-3 at its next free slot and the unpinned runs go to the other node; the task shows
      "waiting for <node>" while it waits.
