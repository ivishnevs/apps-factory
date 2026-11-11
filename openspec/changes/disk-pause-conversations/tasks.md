## 1. Conversations through a disk pause

- [ ] 1.1 Heartbeat answer: `pause` (owner) and `pause_work` (critical disk); the node claims only conversation runs
      under `pause_work` while free space is above `disk.conversation_floor_gib` (default 1 GiB); tests.
- [ ] 1.2 Core claim checks: a disk-paused node may claim a conversation run, never a work or review run; an owner
      pause of the node, company, project or agent still refuses conversations; tests.
- [ ] 1.3 Conversation brief on a disk-paused node says the disk is critical (node, free space, watermark) and that
      new work waits until space is freed; tests.

## 2. An honest reason

- [ ] 2.1 `noCapableNode` / `nodeReason`: a disk pause gives `node_disk_critical` with free space and watermark and the
      clearer "free disk on <node>; it resumes by itself above <watermark>"; an owner pause gives "resume node <node>"
      with the owner's reason; "start or fix a node" only for offline or incapable nodes; tests.
- [ ] 2.2 Web chat waiting line, task page, TUI and Inbox show the new reason; `node.disk_critical` goes to Telegram
      once per episode.

## 3. Docs and verify

- [ ] 3.1 `runtime-protocol-v2.md`, `development.md` (node.yaml), `messaging.md`.
- [ ] 3.2 Replay 2026-11-11: node at 3.6 GiB free (critical 5 GiB), the owner writes to the CEO: the CEO answers, a
      work task stays queued with `node_disk_critical`, and the chat says to free disk on tvorog-mac.
