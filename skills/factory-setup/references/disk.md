# Disk

Goal: the node has room to work.

Watermarks (node.yaml `disk`, defaults): below `low_watermark_gib` 20 GiB the node warns and its janitor prunes
harder; below `critical_watermark_gib` 5 GiB Core pauses the node's work and review runs (conversations still run, so
agents can answer); below `conversation_floor_gib` 1 GiB nothing runs. The pause ends by itself once free space is
above the watermark again: no resume is needed. `factory_doctor` and `factory_why` name the node, its free space and
the watermark.

What may be deleted (on the node, as its user):

- Xcode: `~/Library/Developer/Xcode/DerivedData/*`, `xcrun simctl delete unavailable`, old runtimes in
  `xcrun simctl runtime list`.
- Closed tasks' worktrees: the node's janitor removes them; `factory-node workspace reset -task ID` for one task.
- Package caches: `npm cache clean --force`, `~/Library/Caches/*` of tools you know, Docker `docker system prune`.
- Never delete `~/.app-factory/state` (the node's keys and sessions) or worktrees of open tasks with unpushed work
  (`factory_status` and the Nodes page show tied work).

## Proof

- `df -h` on the disk of `disk.path` shows free space above the critical watermark.
- `factory-node doctor` passes `disk` (or warns only for low).
- `factory_why` for the waiting task no longer says "disk critical", and its run starts (`factory_runs`).
