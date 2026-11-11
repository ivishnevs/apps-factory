Status: proposed 2026-11-11 by the owner ("it is nonsense that the CEO cannot answer; an answer takes no disk
space"). Nothing implemented.

## Why

Seen on 2026-11-11 in tvorog. The only node, `tvorog-mac`, fell to 3.6 GiB free (critical watermark 5 GiB) at 18:24
and stopped claiming. At 18:36 the owner wrote to the CEO in the Wrappie (advent-gift) chat. The message sat unanswered,
and the chat showed only "ceo: start or fix a node with driver claude (node.yaml, factory-node doctor)".

- **A disk pause stops conversations, which do not need the disk.** The critical watermark protects the machine from
  builds, clones and worktrees. A conversation run has no worktree and no build: it runs in the project workspace
  read-only (work-and-conversation-runs) and writes its session file and logs, a few MB. Stopping it takes the owner's
  only way to steer the team away exactly when the owner may need to say "stop, free the disk" or "pause Android".
- **The reason was wrong and so was the fix.** `noCapableNode` lumps an owner pause and a disk pause together as
  "paused", and the clearer for `no_capable_node` is always "start or fix a node with driver claude (node.yaml,
  factory-node doctor)". The node was running and healthy. The real fix (free disk on tvorog-mac; it resumes by
  itself above 5 GiB) was nowhere in the chat, the task or the Inbox.

## What Changes

- **Critical disk pauses work, not conversations.** Below the critical watermark a node stops claiming work and
  review runs as today, but keeps claiming conversation runs while free space stays above a hard floor
  (`disk.conversation_floor_gib`, default 1 GiB). The heartbeat answer splits the pause: `pause` (owner pause: stops
  every claim) and `pause_work` (critical disk: stops all but conversations). An owner pause of the node, the company,
  the project or the agent still stops conversations.
- **A conversation knows the disk is critical.** The brief of a conversation run on a disk-paused node says so (node,
  free space, watermark), so the lead tells the owner that new work will wait until space is freed, instead of
  promising it.
- **The reason names the disk.** The queued verdict tells a disk pause from an owner pause: detail
  "tvorog-mac: disk critical, 3.6 GiB free (critical 5 GiB)" and clearer "free disk on tvorog-mac; it resumes claiming
  by itself above 5 GiB" (`node_disk_critical`). An owner pause says "resume node tvorog-mac (paused by the owner:
  <reason>)". "start or fix a node" stays only for nodes that are offline or lack the driver, labels or resources.
- **The owner hears about it where they wait.** The chat's waiting line and the Inbox alert for a held message name
  the disk and the node, and `node.disk_critical` reaches Telegram once per episode.

## Impact

- Node: disk watermark handling, claim filter by run kind, `node.yaml` `disk.conversation_floor_gib`.
- Core: heartbeat answer (`pause_work`), claim checks, `tasks.noCapableNode` / `nodeReason` verdict and clearer, the
  conversation brief, feed and Telegram for `node.disk_critical`.
- Web, TUI: the waiting line and Nodes page wording.
- Docs: `runtime-protocol-v2.md` (§ heartbeat answer, queued reasons), `development.md` (node.yaml), `messaging.md`.
