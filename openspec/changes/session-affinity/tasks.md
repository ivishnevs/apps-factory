## 1. Decisions (owner)

- [x] 1.1 Release only after the holder is inactive beyond a timeout, never for being busy (owner 2026-11-06).
- [ ] 1.2 Release timeout value (30 minutes proposed), one company setting.

## 2. Core

- [ ] 2.1 Record the node of a session (run's node when the session id is recorded); migration.
- [ ] 2.2 Offers skip runs whose session holder is another node that still holds the affinity; claim refuses with
      retry `affinity`.
- [ ] 2.3 Inactivity timer per lock: starts when the holder goes offline, is paused, removed or unable to run the
      agent; cleared when it is active again; on expiry the lock is released, the next node starts fresh and the
      lock moves. A busy holder never releases.
- [ ] 2.4 Verdict reason `affinity` with the holder and the remaining wait; tests for every release case and for
      two nodes alternating on a conversation, a holder back before the timeout, a busy holder.

## 3. Visibility

- [ ] 3.1 Task: "waiting for NODE, it holds this agent's session"; runs: resumed or fresh, and why.
- [ ] 3.2 Count fresh starts per company over a week before and after.
