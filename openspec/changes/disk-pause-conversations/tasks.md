## 1. Conversations through a disk pause

- [x] 1.1 Heartbeat answer: `pause` (owner) and `pause_work` (critical disk); the node claims only conversation runs
      under `pause_work` while free space is above `disk.conversation_floor_gib` (default 1 GiB); tests.
      Done: Core answers `pause` only for an owner node pause and `pause_work` at `health.disk: critical`
      (`storage.NodeHeartbeat`); the node keeps both (`session.paused`, `pausedWork`), its `DiskGate` judges work
      (critical) and conversations (`Floor`, capped at critical) apart, and the pool's `WorkClaimable` sends no
      free work slots, counts idle work slots for conversations and skips a work offer that slipped through. The
      heartbeat reports `health.disk_conversation_floor_bytes`; a node without it (older) counts as paused for
      conversations too. Tests: `TestDiskGateConversationFloor`, `TestPoolWorkPausedTakesConversations`,
      `TestV2Heartbeat`, `TestNodeLowDiskOnceAndPause`.
- [x] 1.2 Core claim checks: a disk-paused node may claim a conversation run, never a work or review run; an owner
      pause of the node, company, project or agent still refuses conversations; tests.
      Done: `ClaimV2` refuses `retry/disk_critical` (work or review at critical, a conversation below the floor),
      `OffersFor` offers a disk-paused node conversations only; session affinity: a disk-paused holder is inactive
      for work runs (b7fe936) but active for conversations, which it still takes (inactive below the floor).
      Tests: `TestDiskPauseConversations`, `TestDiskPauseSessionAffinity`, `TestDiskPauseOlderNode`.
- [x] 1.3 Conversation brief on a disk-paused node says the disk is critical (node, free space, watermark) and that
      new work waits until space is freed; tests.
      Done: `diskBriefNote` heads the brief of a conversation claimed at a critical disk (`TestDiskPauseConversations`).

## 2. An honest reason

- [x] 2.1 `noCapableNode` / `nodeReason`: a disk pause gives `node_disk_critical` with free space and watermark and the
      clearer "free disk on <node>; it resumes by itself above <watermark>"; an owner pause gives "resume node <node>"
      with the owner's reason; "start or fix a node" only for offline or incapable nodes; tests.
      Done: `tasks.Node` carries `OwnerPaused`/`PauseReason` (Evaluate now sees owner node pauses) and `Disk`;
      a pinned node paused for its disk names the same fix (an owner-paused pinned node keeps today's behaviour:
      the run is queued and its claim refused, the queued reason naming the pause). The queued reason has
      `node_disk_critical` too. Tests: `TestDiskPauseVerdicts`, `TestEvaluateNodeReasons`,
      `TestDiskPauseVerdictOwnerPause`, `TestQueuedWhy`.
- [x] 2.2 Web chat waiting line, task page, TUI and Inbox show the new reason; `node.disk_critical` goes to Telegram
      once per episode.
      Done: the chat waiting line is the conversation task's clearer (`conversationWaiting` names the disk and
      "free disk on <node>"); the task page and board (`humanize`, `model.ts`: label, Blocked column, needs action,
      queued line), the Nodes page ("Answers conversations only; free disk, work resumes by itself above 5 GiB",
      "Work paused") and the TUI (queued label, "work paused") show it. `node.disk_critical` was already an
      attention row once per episode (so it reaches Telegram and the feed); its summary now names the node, free
      space, watermark and the fix. There is no separate Inbox alert for a held message in the code base: the
      Inbox shows messages and approvals only, so the held message's reason is on the chat's waiting line.

## 3. Docs and verify

- [x] 3.1 `runtime-protocol-v2.md`, `development.md` (node.yaml), `messaging.md`. Also `task-lifecycle.md` (session
      affinity) and the `node-agent` and `trigger-scheduling` specs.
- [x] 3.2 Replay 2026-11-11: node at 3.6 GiB free (critical 5 GiB), the owner writes to the CEO: the CEO answers, a
      work task stays queued with `node_disk_critical`, and the chat says to free disk on tvorog-mac.
      Done as an automated replay with Core storage and a node named tvorog-mac (`TestDiskPauseConversations`).
